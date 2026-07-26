package panel

import (
	"net/http"

	"lattix/backend/internal/store"
)

// dashboardDTO 是仪表盘统计（§10：服务器数、在线数、产品层链路数、用户数）。
type dashboardDTO struct {
	Servers       int `json:"servers"`
	ServersOnline int `json:"servers_online"`
	Links         int `json:"links"`
	LinksActive   int `json:"links_active"`
	LinksDegraded int `json:"links_degraded"`
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
	chains, err := s.st.ListChains(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	exitIDs, err := s.st.ChainExitNodeIDs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d.Links = len(chains)
	for _, n := range nodes {
		if exitIDs[n.ID] {
			continue
		}
		d.Links++
		if n.Status == store.NodeStatusActive {
			d.LinksActive++
		}
	}
	for _, chain := range chains {
		if chain.Status == store.ChainStatusActive {
			d.LinksActive++
		}
		if chain.Status == store.ChainStatusDegraded {
			d.LinksDegraded++
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
