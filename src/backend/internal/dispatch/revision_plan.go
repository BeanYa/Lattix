package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"lattix/backend/internal/store"
)

const (
	RevisionPieceService = store.RevisionPieceService
	RevisionPieceForward = store.RevisionPieceForward
	RevisionPiecePortal  = store.RevisionPiecePortal
	RevisionPieceBridge  = store.RevisionPieceBridge
)

// RevisionTopology is the planner input. Hops are ordered entry to exit.
// Settings must contain virtual, not realized, configuration.
type RevisionTopology struct {
	RevisionID int64
	ServiceID  int64
	Service    json.RawMessage
	Hops       []RevisionHopSpec
	// DirectShared means the shared endpoint is also the one-hop exit. There
	// is no separate service listener to deploy in that topology.
	DirectShared bool
}

type RevisionHopSpec struct {
	HopID      int64
	ServerID   int64
	Transport  string
	ListenPort int
	Settings   json.RawMessage
}

type RevisionPiece struct {
	Key         string
	Kind        string
	HopID       int64
	ServerID    int64
	RevisionID  int64
	SpecHash    string
	DependsHash string
}

type RevisionPlan struct {
	Apply   []RevisionPiece
	Reuse   []RevisionPiece
	Cleanup []RevisionPiece
}

// PlanRevision computes a conservative content-addressed diff. A piece is
// reused only when its own normalized spec and its complete downstream
// dependency hash are unchanged. Apply is ordered exit to entry; cleanup is a
// separate post-publish phase ordered entry to exit.
func PlanRevision(current, desired RevisionTopology) (RevisionPlan, error) {
	if err := validateTopology(desired); err != nil {
		return RevisionPlan{}, err
	}
	currentPieces, err := materializeRevision(current)
	if err != nil && current.RevisionID != 0 {
		return RevisionPlan{}, fmt.Errorf("materialize current revision: %w", err)
	}
	desiredPieces, err := materializeRevision(desired)
	if err != nil {
		return RevisionPlan{}, fmt.Errorf("materialize desired revision: %w", err)
	}

	oldByKey := make(map[string]RevisionPiece, len(currentPieces))
	for _, piece := range currentPieces {
		oldByKey[piece.Key] = piece
	}
	newByKey := make(map[string]RevisionPiece, len(desiredPieces))
	plan := RevisionPlan{}
	for _, piece := range desiredPieces {
		newByKey[piece.Key] = piece
		old, ok := oldByKey[piece.Key]
		if ok && old.SpecHash == piece.SpecHash && old.DependsHash == piece.DependsHash {
			plan.Reuse = append(plan.Reuse, piece)
			continue
		}
		plan.Apply = append(plan.Apply, piece)
	}
	for _, piece := range currentPieces {
		if _, ok := newByKey[piece.Key]; !ok {
			plan.Cleanup = append(plan.Cleanup, piece)
		}
	}
	sort.SliceStable(plan.Cleanup, func(i, j int) bool {
		return cleanupRank(plan.Cleanup[i], current) < cleanupRank(plan.Cleanup[j], current)
	})
	return plan, nil
}

func validateTopology(topology RevisionTopology) error {
	if topology.RevisionID <= 0 {
		return fmt.Errorf("revision id must be positive")
	}
	if topology.ServiceID <= 0 {
		return fmt.Errorf("service id must be positive")
	}
	if len(topology.Hops) < 1 || len(topology.Hops) > 4 {
		return fmt.Errorf("chain must contain between 1 and 4 hops")
	}
	if topology.DirectShared && len(topology.Hops) != 1 {
		return fmt.Errorf("direct shared topology must contain exactly one hop")
	}
	seenHop := map[int64]bool{}
	seenServer := map[int64]bool{}
	for _, hop := range topology.Hops {
		if hop.HopID <= 0 || hop.ServerID <= 0 {
			return fmt.Errorf("hop and server ids must be positive")
		}
		if seenHop[hop.HopID] {
			return fmt.Errorf("duplicate hop id %d", hop.HopID)
		}
		if seenServer[hop.ServerID] {
			return fmt.Errorf("duplicate server id %d", hop.ServerID)
		}
		seenHop[hop.HopID] = true
		seenServer[hop.ServerID] = true
		switch hop.Transport {
		case "", "direct", "encrypted", "reverse":
		default:
			return fmt.Errorf("unsupported transport %q", hop.Transport)
		}
	}
	return nil
}

func materializeRevision(topology RevisionTopology) ([]RevisionPiece, error) {
	if err := validateTopology(topology); err != nil {
		return nil, err
	}
	if topology.DirectShared {
		return []RevisionPiece{}, nil
	}
	pieces := make([]RevisionPiece, 0, len(topology.Hops)*3+1)
	exit := topology.Hops[len(topology.Hops)-1]
	serviceHash, err := hashNormalized(map[string]any{
		"service_id": topology.ServiceID,
		"server_id":  exit.ServerID,
		"config":     json.RawMessage(topology.Service),
	})
	if err != nil {
		return nil, err
	}
	downstreamHash := serviceHash
	pieces = append(pieces, RevisionPiece{
		Key: revisionPieceKey(RevisionPieceService, topology.ServiceID), Kind: RevisionPieceService,
		HopID: exit.HopID, ServerID: exit.ServerID, RevisionID: topology.RevisionID,
		SpecHash: serviceHash, DependsHash: serviceHash,
	})

	for i := len(topology.Hops) - 2; i >= 0; i-- {
		hop := topology.Hops[i]
		next := topology.Hops[i+1]
		transport := hop.Transport
		if transport == "" {
			transport = "direct"
		}
		if transport == "reverse" || transport == "encrypted" {
			portalHash, err := hashNormalized(map[string]any{
				"kind": RevisionPiecePortal, "hop_id": hop.HopID, "server_id": hop.ServerID,
				"transport": transport, "settings": json.RawMessage(hop.Settings),
			})
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, RevisionPiece{
				Key: revisionPieceKey(RevisionPiecePortal, hop.HopID), Kind: RevisionPiecePortal,
				HopID: hop.HopID, ServerID: hop.ServerID, RevisionID: topology.RevisionID,
				SpecHash: portalHash, DependsHash: portalHash,
			})
			bridgeHash, err := hashNormalized(map[string]any{
				"kind": RevisionPieceBridge, "hop_id": next.HopID, "server_id": next.ServerID,
				"portal_hash": portalHash, "transport": transport,
			})
			if err != nil {
				return nil, err
			}
			pieces = append(pieces, RevisionPiece{
				Key: revisionPieceKey(RevisionPieceBridge, next.HopID), Kind: RevisionPieceBridge,
				HopID: next.HopID, ServerID: next.ServerID, RevisionID: topology.RevisionID,
				SpecHash: bridgeHash, DependsHash: portalHash,
			})
		}
		forwardHash, err := hashNormalized(map[string]any{
			"kind": RevisionPieceForward, "hop_id": hop.HopID, "server_id": hop.ServerID,
			"next_hop_id": next.HopID, "next_server_id": next.ServerID,
			"transport": transport, "listen_port": hop.ListenPort,
			"settings": json.RawMessage(hop.Settings),
		})
		if err != nil {
			return nil, err
		}
		dependsHash, err := hashNormalized(map[string]any{
			"piece": forwardHash, "downstream": downstreamHash,
		})
		if err != nil {
			return nil, err
		}
		pieces = append(pieces, RevisionPiece{
			Key: revisionPieceKey(RevisionPieceForward, hop.HopID), Kind: RevisionPieceForward,
			HopID: hop.HopID, ServerID: hop.ServerID, RevisionID: topology.RevisionID,
			SpecHash: forwardHash, DependsHash: dependsHash,
		})
		downstreamHash = dependsHash
	}
	return pieces, nil
}

func revisionPieceKey(kind string, id int64) string { return fmt.Sprintf("%s/%d", kind, id) }

func hashNormalized(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func cleanupRank(piece RevisionPiece, topology RevisionTopology) int {
	if piece.Kind == RevisionPieceService {
		return len(topology.Hops)*10 + 9
	}
	for i, hop := range topology.Hops {
		if hop.HopID == piece.HopID {
			kindRank := map[string]int{RevisionPieceForward: 0, RevisionPieceBridge: 1, RevisionPiecePortal: 2}
			return i*10 + kindRank[piece.Kind]
		}
	}
	return len(topology.Hops)*10 + 8
}
