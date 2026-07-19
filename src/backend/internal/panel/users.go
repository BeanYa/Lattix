package panel

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// userDTO 是用户对象的 API 表示。
type userDTO struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	UUID      string    `json:"uuid"`
	SubToken  string    `json:"sub_token"`
	SubURL    string    `json:"sub_url"` // 订阅链接（§9）
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) toUserDTO(r *http.Request, u store.User) userDTO {
	return userDTO{
		ID:        u.ID,
		Name:      u.Name,
		UUID:      u.UUID,
		SubToken:  u.SubToken,
		SubURL:    fmt.Sprintf("%s/sub/%s", s.panelBase(r), u.SubToken),
		CreatedAt: u.CreatedAt,
	}
}

// handleListUsers 处理 GET /api/users。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, s.toUserDTO(r, u))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateUser 处理 POST /api/users：生成 UUID 与 sub_token，
// 并向所有服务器扇出 add_user（在线立即下发，离线留 commands 队列补发，§8）。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, "name 不能为空")
		return
	}
	u := store.User{
		Name:     req.Name,
		UUID:     uuid.NewString(),
		SubToken: randomHex(16),
	}
	id, err := s.st.InsertUser(r.Context(), u.Name, u.UUID, u.SubToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.fanout(r, shared.TypeAddUser, shared.AddUserPayload{UUID: u.UUID})

	created, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.toUserDTO(r, *created))
}

// handleDeleteUser 处理 DELETE /api/users/{id}：扇出 remove_user 后删除（§8）。
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.st.UserByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.fanout(r, shared.TypeRemoveUser, shared.RemoveUserPayload{UUID: u.UUID})
	if err := s.st.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fanout 向所有服务器下发命令（离线服务器留 commands 队列，重连补发，§8）。
func (s *Server) fanout(r *http.Request, typ string, payload any) {
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		return
	}
	for _, srv := range servers {
		if _, err := s.disp.Enqueue(r.Context(), srv.ID, typ, payload); err != nil {
			continue // 单台失败不阻塞其他服务器（错误留在 commands 表）
		}
	}
}
