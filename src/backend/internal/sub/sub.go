// Package sub 实现订阅端点（设计文档 §9）：
// GET /sub/{sub_token} → mihomo（Clash.Meta）格式 YAML；浏览器（Accept 含 text/html）→ 落地页 HTML。
package sub

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/store"
	"lattix/shared"
)

// Server 实现订阅端点。
type Server struct {
	st   *store.Store
	base func(*http.Request) string // 面板对外地址（落地页绝对链接，同 panel.PanelBase 判定链）
}

// New 创建订阅服务；base 返回请求对应的面板对外地址（可为 nil，落地页退回请求推断）。
func New(st *store.Store, base func(*http.Request) string) *Server {
	if base == nil {
		base = func(r *http.Request) string {
			scheme := "http"
			if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
				scheme = "https"
			}
			return fmt.Sprintf("%s://%s", scheme, r.Host)
		}
	}
	return &Server{st: st, base: base}
}

// setSubHeaders 写订阅通用响应头（§9）：
// subscription-userinfo（upload/download 取 traffic 表用户维度 node_id=0 的累计值；
// 用户设了有效期才带 expire，unix 秒；项目无流量配额，无 total 字段）与
// profile-update-interval（小时，客户端按天自动刷新订阅）。
func (s *Server) setSubHeaders(w http.ResponseWriter, r *http.Request, user *store.User) {
	t, err := s.st.UserTraffic(r.Context(), user.UUID)
	if err != nil {
		t = store.TrafficTotals{} // 统计查询失败不阻断订阅
	}
	v := fmt.Sprintf("upload=%d; download=%d", t.Up, t.Down)
	if user.ExpiresAt != nil {
		v += fmt.Sprintf("; expire=%d", user.ExpiresAt.Unix())
	}
	w.Header().Set("Subscription-Userinfo", v)
	w.Header().Set("Profile-Update-Interval", "24")
}

type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id,omitempty"`
}

type clashGrpcOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
}

type clashXHTTPOpts struct {
	Path string `yaml:"path,omitempty"`
	Mode string `yaml:"mode,omitempty"`
	Host string `yaml:"host,omitempty"`
}

// clashProxy 是 mihomo 代理项的并集结构：按协议填充相关字段（omitempty 裁剪）。
type clashProxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   int    `yaml:"port"`

	UUID     string `yaml:"uuid,omitempty"`     // vless / vmess
	AlterID  *int   `yaml:"alterId,omitempty"`  // vmess（mihomo 客户端字段，服务端已废弃）
	Cipher   string `yaml:"cipher,omitempty"`   // vmess=auto / ss=method
	Password string `yaml:"password,omitempty"` // trojan / ss / socks / http
	Username string `yaml:"username,omitempty"` // socks / http

	Network    string `yaml:"network,omitempty"`
	TLS        bool   `yaml:"tls,omitempty"`
	Servername string `yaml:"servername,omitempty"` // vless / vmess
	SNI        string `yaml:"sni,omitempty"`        // trojan
	Flow       string `yaml:"flow,omitempty"`       // vless
	Encryption string `yaml:"encryption,omitempty"` // vless（VLESS Encryption 客户端字符串）
	UDP        bool   `yaml:"udp"`

	RealityOpts       *clashRealityOpts `yaml:"reality-opts,omitempty"`
	ClientFingerprint string            `yaml:"client-fingerprint,omitempty"`
	GrpcOpts          *clashGrpcOpts    `yaml:"grpc-opts,omitempty"`
	XhttpOpts         *clashXHTTPOpts   `yaml:"xhttp-opts,omitempty"`
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

// assignedActiveNodes 返回订阅用户及其分配到的 active 节点（§16 公共查询）。
func (s *Server) assignedActiveNodes(r *http.Request) (*store.User, []store.Node, error) {
	user, err := s.st.UserBySubToken(r.Context(), r.PathValue("token"))
	if err != nil {
		return nil, nil, err
	}
	nodes, err := s.st.ListNodes(r.Context())
	if err != nil {
		return nil, nil, err
	}
	assigned, err := s.st.UserNodeIDs(r.Context(), user.ID)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[int64]bool, len(assigned))
	for _, id := range assigned {
		allowed[id] = true
	}
	out := []store.Node{}
	for _, n := range nodes {
		if !allowed[n.ID] {
			continue
		}
		if n.Status != store.NodeStatusActive || len(n.RealizedConfig) == 0 {
			continue
		}
		out = append(out, n)
	}
	return user, out, nil
}

// ServeHTTP 处理 GET /sub/{token}：按该用户自己的 UUID 为每个 active 节点生成一项代理（§9）。
// Accept 含 text/html（浏览器）时返回订阅落地页 HTML；否则返回 mihomo YAML。
// 有效停权态（expired=1 或 disabled=1，§9/§16）的用户订阅照常返回但 proxies 为空——
// 客户端显示到期/停用而不是报错。dokodemo-door 为端口转发，客户端无法作为代理消费，不进订阅。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, nodes, err := s.assignedActiveNodes(r)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error()+"\n", status)
		return
	}
	s.setSubHeaders(w, r, user)

	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		s.serveLanding(w, r, user, nodes)
		return
	}

	if user.Expired || user.Disabled {
		nodes = nil // 有效停权态（§9/§16）：proxies 为空
	}
	cfg := clashConfig{Proxies: []clashProxy{}}
	names := []string{}
	for _, it := range s.subscriptionItems(r, user, nodes) {
		if it.node.Protocol == shared.ProtocolDokodemo {
			continue
		}
		p, err := buildProxy(it.node, it.rc, user.UUID)
		if err != nil {
			continue
		}
		cfg.Proxies = append(cfg.Proxies, p)
		names = append(names, p.Name)
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

// proxyItem 是一个订阅条目的来源：节点行 + 生效值
// （链条目已把别名/地址/端口替换为入口侧，§21；其余字段取出口 realized_config）。
type proxyItem struct {
	node store.Node
	rc   shared.RealizedConfig
}

// subscriptionItems 汇总单机节点与链条目（§21 订阅）：
//   - 链出口业务节点不再作为单机条目出现（只能经链入口消费）；
//   - 链条目：server/port 取入口（非 1:1 映射时经端口段助手换算 public 端口），
//     reality-opts/uuid/flow 等取出口节点 realized_config；命名沿用 {入口别名}-{协议}-{端口}；
//   - 只含 active/degraded 链（failed/pending/applying 不出）；degraded 不剔除（客户端测速规避）；
//   - 用户维度经 user_nodes 判出口节点分配（§16：UUID 只存在于出口 xray）。
func (s *Server) subscriptionItems(r *http.Request, user *store.User, nodes []store.Node) []proxyItem {
	exitIDs, err := s.st.ChainExitNodeIDs(r.Context())
	if err != nil {
		exitIDs = map[int64]bool{} // 查询失败不阻断订阅
	}
	items := []proxyItem{}
	for _, n := range nodes {
		if exitIDs[n.ID] {
			continue
		}
		var rc shared.RealizedConfig
		if err := json.Unmarshal(n.RealizedConfig, &rc); err != nil {
			continue // 生效值损坏的节点跳过（异常留在 nodes 表）
		}
		items = append(items, proxyItem{node: n, rc: rc})
	}
	assigned, err := s.st.UserNodeIDs(r.Context(), user.ID)
	if err != nil {
		return items
	}
	allowed := make(map[int64]bool, len(assigned))
	for _, id := range assigned {
		allowed[id] = true
	}
	chains, err := s.st.ListChains(r.Context())
	if err != nil {
		return items
	}
	for _, c := range chains {
		if c.Status != store.ChainStatusActive && c.Status != store.ChainStatusDegraded {
			continue
		}
		item, err := s.chainSubscriptionItem(r, c.ID, allowed)
		if err != nil {
			continue
		}
		items = append(items, *item)
	}
	return items
}

// chainSubscriptionItem 构造单条链的订阅条目；不满足输出条件返回错误（调用方跳过）。
func (s *Server) chainSubscriptionItem(r *http.Request, chainID int64, allowed map[int64]bool) (*proxyItem, error) {
	hops, err := s.st.ChainHops(r.Context(), chainID)
	if err != nil {
		return nil, err
	}
	if len(hops) < 2 {
		return nil, fmt.Errorf("链 %d 跳数不足", chainID)
	}
	entry, exit := hops[0], hops[len(hops)-1]
	if !allowed[exit.NodeID] {
		return nil, fmt.Errorf("链 %d 出口节点未分配给该用户", chainID)
	}
	node, err := s.st.NodeByID(r.Context(), exit.NodeID)
	if err != nil {
		return nil, err
	}
	if node.Status != store.NodeStatusActive || len(node.RealizedConfig) == 0 {
		return nil, fmt.Errorf("链 %d 出口节点 %d 未生效", chainID, exit.NodeID)
	}
	var rc shared.RealizedConfig
	if err := json.Unmarshal(node.RealizedConfig, &rc); err != nil || rc.Port == 0 {
		return nil, fmt.Errorf("链 %d 出口 realized_config 不可用", chainID)
	}
	entrySrv, err := s.st.ServerByID(r.Context(), entry.ServerID)
	if err != nil {
		return nil, err
	}
	if entrySrv.Address == "" || entry.ForwardPort == 0 {
		return nil, fmt.Errorf("链 %d 入口地址/端口未就绪", chainID)
	}
	// 订阅端口取入口公网侧（非 1:1 映射时经端口段助手换算，§21）。
	port := entry.ForwardPort
	if ranges, err := shared.ParsePortRanges(entrySrv.AllowedPorts); err == nil && len(ranges) > 0 {
		if pub, ok := shared.PublicPort(ranges, port); ok {
			port = pub
		}
	}
	entryNode := *node
	entryNode.ServerAlias = entrySrv.Alias
	entryNode.ServerAddress = entrySrv.Address
	rc.Port = port
	return &proxyItem{node: entryNode, rc: rc}, nil
}

// buildProxy 按节点协议构造 mihomo 代理项；uuid 为该订阅用户自己的 UUID（§8/§9）。
// 节点命名：{服务器别名}-{协议}-{端口}。
func buildProxy(n store.Node, rc shared.RealizedConfig, uuid string) (clashProxy, error) {
	if rc.Port == 0 {
		return clashProxy{}, fmt.Errorf("节点 %d 缺少生效端口", n.ID)
	}
	// 兼容存量 realized_config：缺省 network=tcp、fingerprint=chrome。
	if rc.Network == "" {
		rc.Network = shared.NetworkTCP
	}
	if rc.Fingerprint == "" {
		rc.Fingerprint = shared.FingerprintChrome
	}
	p := clashProxy{
		Name:   fmt.Sprintf("%s-%s-%d", n.ServerAlias, n.Protocol, rc.Port),
		Type:   n.Protocol,
		Server: n.ServerAddress,
		Port:   rc.Port,
		UDP:    true,
	}
	switch n.Protocol {
	case shared.ProtocolVLESS:
		p.UUID = uuid
		p.Network = rc.Network
		p.TLS = true
		p.Servername = rc.ServerName
		p.Flow = rc.Flow
		p.Encryption = rc.Encryption
		applyReality(&p, rc)
	case shared.ProtocolVMess:
		zero := 0
		p.UUID = uuid
		p.AlterID = &zero
		p.Cipher = "auto"
		p.Network = rc.Network
		p.TLS = true
		p.Servername = rc.ServerName
		applyReality(&p, rc)
	case shared.ProtocolTrojan:
		p.Password = uuid
		p.Network = rc.Network
		p.SNI = rc.ServerName
		applyReality(&p, rc)
	case shared.ProtocolShadowsocks:
		p.Type = "ss" // mihomo 类型名
		p.Cipher = rc.Method
		if shared.Is2022Method(rc.Method) {
			// 2022-blake3 多用户：客户端密码为 "节点PSK:用户密钥"。
			p.Password = rc.PSK + ":" + shared.SSUserPassword(uuid, rc.Method)
		} else {
			p.Password = shared.SSUserPassword(uuid, rc.Method)
		}
	case shared.ProtocolSocks:
		p.Type = "socks5"
		p.Username = uuid
		p.Password = uuid
	case shared.ProtocolHTTP:
		p.Username = uuid
		p.Password = uuid
		p.UDP = false
	default:
		return clashProxy{}, fmt.Errorf("未知协议: %s", n.Protocol)
	}
	return p, nil
}

// applyReality 填充 reality 系协议共用的 TLS 指纹、reality-opts 与传输选项。
func applyReality(p *clashProxy, rc shared.RealizedConfig) {
	p.ClientFingerprint = rc.Fingerprint
	p.RealityOpts = &clashRealityOpts{PublicKey: rc.PublicKey, ShortID: rc.ShortID}
	switch rc.Network {
	case shared.NetworkGRPC:
		p.GrpcOpts = &clashGrpcOpts{ServiceName: rc.ServiceName}
	case shared.NetworkXHTTP:
		p.XhttpOpts = &clashXHTTPOpts{Path: rc.Path, Mode: rc.Mode, Host: rc.Host}
	}
}
