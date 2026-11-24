package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrTemplateConflict = errors.New("模板或关联链路已变化，请刷新后重试")

type OrchestrationNodeInput struct {
	Kind       string `json:"kind"`
	ServerID   int64  `json:"server_id,omitempty"`
	IsRoot     bool   `json:"is_root,omitempty"`
	Address    string `json:"address,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
	Subject    string `json:"subject,omitempty"`
	Action     string `json:"action,omitempty"`
	Priority   int    `json:"priority,omitempty"`
}
type OrchestrationEdgeInput struct {
	From int `json:"from"`
	To   int `json:"to"`
}
type OrchestrationGraph struct {
	Nodes []OrchestrationNodeInput `json:"nodes"`
	Edges []OrchestrationEdgeInput `json:"edges"`
}
type OrchestrationTemplate struct {
	OrchestrationGraph
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Revision   int       `json:"revision"`
	UsageCount int       `json:"usage_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
type ParamOverride struct {
	ServerID   int64   `json:"server_id"`
	Address    *string `json:"address,omitempty"`
	ListenPort *int    `json:"listen_port,omitempty"`
}
type OrchestrationBinding struct {
	TemplateID     int64           `json:"template_id"`
	TemplateName   string          `json:"template_name"`
	Revision       int             `json:"template_revision"`
	SourceRevision int             `json:"template_source_revision"`
	Overrides      []ParamOverride `json:"overrides"`
}
type HopGraphWrite struct {
	Graph   ChainHopGraph
	Binding *OrchestrationBinding
}
type TemplatePropagation struct {
	Graph       ChainHopGraph
	BaseAddress string
	BasePort    int
	Redeploy    bool
	PublishedID int64
}

const templateSelect = `SELECT id,name,revision,graph_json,created_at,updated_at,
 (SELECT COUNT(DISTINCT chain_id) FROM orchestration_bindings WHERE template_id=t.id) FROM orchestration_templates t`

func scanTemplate(row interface{ Scan(...any) error }) (OrchestrationTemplate, error) {
	var t OrchestrationTemplate
	var raw string
	err := row.Scan(&t.ID, &t.Name, &t.Revision, &raw, &t.CreatedAt, &t.UpdatedAt, &t.UsageCount)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &t.OrchestrationGraph)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return t, err
}
func (s *Store) OrchestrationTemplate(ctx context.Context, id int64) (OrchestrationTemplate, error) {
	return scanTemplate(s.db.QueryRowContext(ctx, templateSelect+" WHERE id=?", id))
}
func (s *Store) OrchestrationTemplates(ctx context.Context) ([]OrchestrationTemplate, error) {
	rows, err := s.db.QueryContext(ctx, templateSelect+" ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []OrchestrationTemplate{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
func clearOrchestrationBindingTx(ctx context.Context, tx *sql.Tx, chainID int64, seq int) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM orchestration_bindings WHERE chain_id=? AND seq=?`, chainID, seq); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM orchestration_sync WHERE chain_id=? AND seq=?`, chainID, seq)
	return err
}
func writeHopGraphsTx(ctx context.Context, tx *sql.Tx, chainID int64, writes []HopGraphWrite) error {
	for _, w := range writes {
		g := w.Graph
		if err := clearOrchestrationBindingTx(ctx, tx, chainID, g.Seq); err != nil {
			return err
		}
		if err := replaceChainHopGraphTx(ctx, tx, chainID, g.Seq, g.Nodes, g.Edges); err != nil {
			return err
		}
		if b := w.Binding; b != nil {
			raw, err := json.Marshal(b.Overrides)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO orchestration_bindings(chain_id,seq,template_id,template_revision,overrides)
 SELECT ?,?,id,revision,? FROM orchestration_templates WHERE id=? AND revision=?`, chainID, g.Seq, string(raw), b.TemplateID, b.Revision)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrTemplateConflict
			}
		}
	}
	return nil
}
func (s *Store) loadOrchestrationBindings(ctx context.Context, chainID int64, graphs map[int]ChainHopGraph) error {
	rows, err := s.db.QueryContext(ctx, `SELECT g.seq,COALESCE(t.id,0),COALESCE(t.name,''),COALESCE(b.template_revision,0),COALESCE(b.overrides,'[]'),p.seq IS NOT NULL,COALESCE(p.target_revision,b.template_revision,0)
 FROM chain_hop_graphs g LEFT JOIN orchestration_bindings b USING(chain_id,seq)
 LEFT JOIN orchestration_templates t ON t.id=b.template_id LEFT JOIN orchestration_sync p USING(chain_id,seq) WHERE g.chain_id=?`, chainID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var b OrchestrationBinding
		var raw string
		var pending bool
		if err := rows.Scan(&seq, &b.TemplateID, &b.TemplateName, &b.Revision, &raw, &pending, &b.SourceRevision); err != nil {
			return err
		}
		g, ok := graphs[seq]
		if !ok {
			continue
		}
		g.SyncPending = pending
		if b.TemplateID != 0 {
			if err := json.Unmarshal([]byte(raw), &b.Overrides); err != nil {
				return err
			}
			g.Binding = &b
		}
		graphs[seq] = g
	}
	return rows.Err()
}

type TemplateUse struct {
	ChainID     int64
	Seq         int
	Overrides   []ParamOverride
	BaseAddress *string
	BasePort    *int
}

func (s *Store) TemplateUses(ctx context.Context, id int64) ([]TemplateUse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.chain_id,b.seq,b.overrides,p.base_address,p.base_port FROM orchestration_bindings b LEFT JOIN orchestration_sync p USING(chain_id,seq) WHERE b.template_id=? ORDER BY b.chain_id,b.seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uses []TemplateUse
	for rows.Next() {
		var use TemplateUse
		var raw string
		if err := rows.Scan(&use.ChainID, &use.Seq, &raw, &use.BaseAddress, &use.BasePort); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &use.Overrides); err != nil {
			return nil, err
		}
		uses = append(uses, use)
	}
	return uses, rows.Err()
}
func (s *Store) SaveOrchestrationTemplate(ctx context.Context, t OrchestrationTemplate, updates []TemplatePropagation) (OrchestrationTemplate, error) {
	raw, err := json.Marshal(t.OrchestrationGraph)
	if err != nil {
		return t, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	if t.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO orchestration_templates(name,revision,graph_json) VALUES(?,1,?)`, t.Name, string(raw))
		if err != nil {
			return t, err
		}
		t.ID, err = res.LastInsertId()
		if err != nil {
			return t, err
		}
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE orchestration_templates SET name=?,graph_json=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP WHERE id=? AND revision=? AND (SELECT COUNT(*) FROM orchestration_bindings WHERE template_id=?)=?`, t.Name, string(raw), t.ID, t.Revision, t.ID, len(updates))
		if err != nil {
			return t, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return t, ErrTemplateConflict
		}
	}
	for _, u := range updates {
		g := u.Graph
		var ready bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chains c JOIN orchestration_bindings b ON b.chain_id=c.id WHERE c.id=? AND b.seq=? AND b.template_id=? AND COALESCE(c.desired_revision_id,0)=0 AND COALESCE(c.published_revision_id,0)=? AND c.status IN ('active','degraded'))`, g.ChainID, g.Seq, t.ID, u.PublishedID).Scan(&ready); err != nil {
			return t, err
		}
		if !ready {
			return t, fmt.Errorf("链路 #%d: %w", g.ChainID, ErrTemplateConflict)
		}
		if err := replaceChainHopGraphTx(ctx, tx, g.ChainID, g.Seq, g.Nodes, g.Edges); err != nil {
			return t, err
		}
		if u.Redeploy {
			_, err = tx.ExecContext(ctx, `INSERT INTO orchestration_sync(chain_id,seq,base_address,base_port,target_revision) VALUES(?,?,?,?,?) ON CONFLICT(chain_id,seq) DO UPDATE SET target_revision=excluded.target_revision,deployment_id=0`, g.ChainID, g.Seq, u.BaseAddress, u.BasePort, t.Revision+1)
		} else {
			if _, err = tx.ExecContext(ctx, `DELETE FROM orchestration_sync WHERE chain_id=? AND seq=?`, g.ChainID, g.Seq); err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE orchestration_bindings SET template_revision=? WHERE chain_id=? AND seq=?`, t.Revision+1, g.ChainID, g.Seq)
			}
		}
		if err != nil {
			return t, err
		}
	}
	if err := tx.Commit(); err != nil {
		return t, err
	}
	return s.OrchestrationTemplate(ctx, t.ID)
}

type TemplateDeleteExpectation struct {
	Revision int
	Uses     []TemplateUse
}

func (s *Store) DeleteOrchestrationTemplate(ctx context.Context, id int64, expected TemplateDeleteExpectation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision, count int
	if err := tx.QueryRowContext(ctx, `SELECT revision,(SELECT COUNT(*) FROM orchestration_bindings WHERE template_id=?) FROM orchestration_templates WHERE id=?`, id, id).Scan(&revision, &count); err != nil {
		return err
	}
	if revision != expected.Revision || count != len(expected.Uses) {
		return ErrTemplateConflict
	}
	for _, use := range expected.Uses {
		var present bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orchestration_bindings WHERE template_id=? AND chain_id=? AND seq=?)`, id, use.ChainID, use.Seq).Scan(&present); err != nil {
			return err
		}
		if !present {
			return ErrTemplateConflict
		}
	}
	// Each runtime graph already contains its resolved values.
	if _, err := tx.ExecContext(ctx, `DELETE FROM orchestration_bindings WHERE template_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM orchestration_templates WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
