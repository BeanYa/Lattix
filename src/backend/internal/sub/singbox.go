package sub

import (
	"encoding/json"
	"fmt"
	"net/http"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// sing-box 远程配置结构（预留协议扩展分支，首版 VLESS+Reality）。

type sbTLSReality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id,omitempty"`
}

type sbUTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type sbTLS struct {
	Enabled    bool          `json:"enabled"`
	ServerName string        `json:"server_name,omitempty"`
	Reality    *sbTLSReality `json:"reality,omitempty"`
	UTLS       *sbUTLS       `json:"utls,omitempty"`
}

type sbTransport struct {
	Type        string `json:"type"`
	ServiceName string `json:"service_name,omitempty"` // grpc
	Path        string `json:"path,omitempty"`         // xhttp/ws
	Mode        string `json:"mode,omitempty"`         // xhttp
	Host        string `json:"host,omitempty"`         // xhttp
}

type sbOutbound struct {
	Type       string       `json:"type"`
	Tag        string       `json:"tag"`
	Server     string       `json:"server,omitempty"`
	ServerPort int          `json:"server_port,omitempty"`
	UUID       string       `json:"uuid,omitempty"`
	Flow       string       `json:"flow,omitempty"`
	Encryption string       `json:"encryption,omitempty"`
	Password   string       `json:"password,omitempty"`
	Method     string       `json:"method,omitempty"` // shadowsocks
	TLS        *sbTLS       `json:"tls,omitempty"`
	Transport  *sbTransport `json:"transport,omitempty"`
	Outbounds  []string     `json:"outbounds,omitempty"` // selector
}

type sbRoute struct {
	Final string `json:"final"`
}

type sbConfig struct {
	Outbounds []sbOutbound `json:"outbounds"`
	Route     sbRoute      `json:"route"`
}

// serveSingbox 输出 sing-box JSON 远程配置。
func (s *Server) serveSingbox(w http.ResponseWriter, r *http.Request, user *store.User, items []proxyItem) {
	cfg := sbConfig{
		Outbounds: []sbOutbound{},
		Route:     sbRoute{Final: proxyGroupName},
	}
	tags := []string{}
	for _, it := range items {
		credential := it.credential
		if credential == "" {
			credential = user.UUID
		}
		ob, err := buildSbOutbound(it.node, it.rc, credential)
		if err != nil {
			continue
		}
		cfg.Outbounds = append(cfg.Outbounds, ob)
		tags = append(tags, ob.Tag)
	}
	// selector 组
	cfg.Outbounds = append(cfg.Outbounds, sbOutbound{
		Type:      "selector",
		Tag:       proxyGroupName,
		Outbounds: tags,
	})

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(out)
}

// buildSbOutbound 按协议构造 sing-box outbound；预留协议分支。
func buildSbOutbound(n store.Node, rc shared.RealizedConfig, uuid string) (sbOutbound, error) {
	if rc.Port == 0 {
		return sbOutbound{}, fmt.Errorf("节点 %d 缺少生效端口", n.ID)
	}
	if rc.Network == "" {
		rc.Network = shared.NetworkTCP
	}
	if rc.Fingerprint == "" {
		rc.Fingerprint = shared.FingerprintChrome
	}
	name := nodeName(n, rc)

	ob := sbOutbound{
		Tag:        name,
		Server:     n.ServerAddress,
		ServerPort: rc.Port,
	}

	switch n.Protocol {
	case shared.ProtocolVLESS:
		ob.Type = "vless"
		ob.UUID = uuid
		ob.Flow = rc.Flow
		if rc.Encryption != "" {
			ob.Encryption = rc.Encryption
		} else {
			ob.Encryption = "none"
		}
		ob.TLS = buildSbTLS(rc)
		ob.Transport = buildSbTransport(rc)
	case shared.ProtocolTrojan:
		ob.Type = "trojan"
		ob.Password = uuid
		ob.TLS = buildSbTLS(rc)
		ob.Transport = buildSbTransport(rc)
	case shared.ProtocolVMess:
		ob.Type = "vmess"
		ob.UUID = uuid
		ob.TLS = buildSbTLS(rc)
		ob.Transport = buildSbTransport(rc)
	case shared.ProtocolShadowsocks:
		ob.Type = "shadowsocks"
		ob.Method = rc.Method
		if shared.Is2022Method(rc.Method) {
			ob.Password = rc.PSK + ":" + shared.SSUserPassword(uuid, rc.Method)
		} else {
			ob.Password = shared.SSUserPassword(uuid, rc.Method)
		}
	default:
		return sbOutbound{}, fmt.Errorf("sing-box 不支持协议: %s", n.Protocol)
	}
	return ob, nil
}

func buildSbTLS(rc shared.RealizedConfig) *sbTLS {
	tls := &sbTLS{
		Enabled:    true,
		ServerName: rc.ServerName,
		Reality: &sbTLSReality{
			Enabled:   true,
			PublicKey: rc.PublicKey,
			ShortID:   rc.ShortID,
		},
		UTLS: &sbUTLS{
			Enabled:     true,
			Fingerprint: rc.Fingerprint,
		},
	}
	return tls
}

func buildSbTransport(rc shared.RealizedConfig) *sbTransport {
	switch rc.Network {
	case shared.NetworkGRPC:
		return &sbTransport{Type: "grpc", ServiceName: rc.ServiceName}
	case shared.NetworkXHTTP:
		return &sbTransport{Type: "xhttp", Path: rc.Path, Mode: rc.Mode, Host: rc.Host}
	default:
		return nil // tcp/raw 不需要 transport
	}
}
