package panel

import (
	"net/http"

	"lattix/backend/internal/store"
)

// dashboardDTO 是仪表盘统计（§10：服务器数、在线数、节点数、用户数）。
type dashboardDTO struct {
	Servers       int `json:"servers"`
	ServersOnline int `json:"servers_online"`
	Nodes         int `json:"nodes"`
	NodesActive   int `json:"nodes_active"`
	Users         int `json:"users"`
}

// handleDashboard 处理 GET /api/dashboard。
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	var d dashboardDTO
	servers, err := s.st.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Servers = len(servers)
	for _, srv := range servers {
		if s.req.IsOnline(srv.ID) {
			d.ServersOnline++
		}
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Nodes = len(nodes)
	for _, n := range nodes {
		if n.Status == store.NodeStatusActive {
			d.NodesActive++
		}
	}
	users, err := s.st.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Users = len(users)
	writeJSON(w, http.StatusOK, d)
}
