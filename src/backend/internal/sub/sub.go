// Package sub 实现订阅端点（设计文档 §9）：
// GET /sub/{sub_token} → mihomo（Clash.Meta）格式 YAML。
package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// Server 实现订阅端点。
type Server struct {
	st *store.Store
}

// New 创建订阅服务。
func New(st *store.Store) *Server {
	return &Server{st: st}
}

type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

type clashProxy struct {
	Name              string           `yaml:"name"`
	Type              string           `yaml:"type"`
	Server            string           `yaml:"server"`
	Port              int              `yaml:"port"`
	UUID              string           `yaml:"uuid"`
	Network           string           `yaml:"network"`
	TLS               bool             `yaml:"tls"`
	UDP               bool             `yaml:"udp"`
	Flow              string           `yaml:"flow"`
	Servername        string           `yaml:"servername,omitempty"`
	RealityOpts       clashRealityOpts `yaml:"reality-opts"`
	ClientFingerprint string           `yaml:"client-fingerprint"`
}

type clashProxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
}

type clashConfig struct {
	Proxies     []clashProxy      `yaml:"proxies"`
	ProxyGroups []clashProxyGroup `yaml:"proxy-groups"`
	Rules       []string          `yaml:"rules"`
}

// proxyGroupName 是 select 组名，MATCH 规则指向它。
const proxyGroupName = "PROXY"

// ServeHTTP 处理 GET /sub/{token}：按该用户自己的 UUID 为每个 active 节点生成一项 vless 代理（§9）。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "subscription not found\n", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}

	cfg := clashConfig{Proxies: []clashProxy{}}
	names := []string{}
	for _, n := range nodes {
		if n.Status != store.NodeStatusActive || len(n.RealizedConfig) == 0 {
			continue
		}
		var rc shared.RealizedConfig
		if err := json.Unmarshal(n.RealizedConfig, &rc); err != nil {
			continue // 生效值损坏的节点跳过（异常留在 nodes 表）
		}
		if rc.Flow == "" {
			rc.Flow = shared.FlowVision
		}
		if rc.Fingerprint == "" {
			rc.Fingerprint = shared.FingerprintChrome
		}
		// 节点命名：{服务器别名}-vless-{端口}（§9）。
		name := fmt.Sprintf("%s-vless-%d", n.ServerAlias, rc.Port)
		cfg.Proxies = append(cfg.Proxies, clashProxy{
			Name:              name,
			Type:              "vless",
			Server:            n.ServerAddress,
			Port:              rc.Port,
			UUID:              user.UUID, // 嵌入该用户自己的 UUID（§9）
			Network:           "tcp",
			TLS:               true,
			UDP:               true,
			Flow:              rc.Flow,
			Servername:        rc.ServerName,
			RealityOpts:       clashRealityOpts{PublicKey: rc.PublicKey, ShortID: rc.ShortID},
			ClientFingerprint: rc.Fingerprint,
		})
		names = append(names, name)
	}
	cfg.ProxyGroups = []clashProxyGroup{{Name: proxyGroupName, Type: "select", Proxies: names}}
	cfg.Rules = []string{"MATCH," + proxyGroupName}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Write(out)
}
