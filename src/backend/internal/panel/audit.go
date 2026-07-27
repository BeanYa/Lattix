package panel

import (
	"context"
	"log"
	"net/http"
	"strings"

	"lattix/backend/internal/logging"
)

// audit 记录一条操作日志。
// 在写操作成功路径上调用（失败路径已 writeError 提前返回）；审计写入失败仅记日志，
// 不阻断业务——审计是旁路，不应让记日志失败回滚已成功的业务操作。
//
// action 语义形如 "server.create" / "node.delete" / "settings.update" / "admin.login"；
// serverID/nodeID 为 nil 表示无关联对象；detail 任意值（map/struct/string）会 json.Marshal。
func (s *Server) audit(r *http.Request, action string, serverID, nodeID *int64, detail any) {
	operator, _ := s.currentUser(r)
	if err := s.recordOperation(r.Context(), logging.OperationEvent{
		Severity:  severityForAction(action),
		Category:  categoryForAction(action),
		Action:    action,
		ServerID:  serverID,
		NodeID:    nodeID,
		Detail:    detail,
		Operator:  operator,
		IP:        logging.ClientIP(r),
		RequestID: logging.RequestID(r.Context()),
	}); err != nil {
		log.Printf("panel: audit %s: %v", action, err)
	}
}

func (s *Server) recordOperation(ctx context.Context, event logging.OperationEvent) error {
	if s.opLog == nil {
		return nil
	}
	return s.opLog.Record(ctx, event)
}

func categoryForAction(action string) logging.Category {
	switch {
	case strings.HasPrefix(action, "server."):
		return logging.CategoryServer
	case strings.HasPrefix(action, "chain."), strings.HasPrefix(action, "node."):
		return logging.CategoryChain
	case strings.HasPrefix(action, "user."):
		return logging.CategoryUser
	case strings.HasPrefix(action, "settings."):
		return logging.CategorySettings
	case strings.HasPrefix(action, "panel."), strings.HasPrefix(action, "admin.restart"):
		return logging.CategoryPanel
	case strings.HasPrefix(action, "agent."):
		return logging.CategoryAgent
	case strings.HasPrefix(action, "command."):
		return logging.CategoryCommand
	case strings.HasPrefix(action, "auth."), strings.HasPrefix(action, "admin.login"),
		strings.HasPrefix(action, "admin.logout"), strings.HasPrefix(action, "admin.change_password"):
		return logging.CategoryAuth
	default:
		return logging.CategoryLog
	}
}

func severityForAction(action string) logging.Severity {
	switch {
	case strings.Contains(action, "failed"), strings.Contains(action, "error"):
		return logging.SeverityError
	case strings.Contains(action, "offline"), strings.Contains(action, "degraded"),
		strings.Contains(action, "drift"), strings.Contains(action, "login_failed"),
		strings.Contains(action, "restart"), strings.Contains(action, "update_started"):
		return logging.SeverityWarning
	default:
		return logging.SeverityInfo
	}
}
