package xray

import (
	"encoding/json"
	"strings"

	"lattix/shared"
)

// fullConfig 是 config.json 的浅层表示：顶层键保留原始 JSON，
// inbounds 数组元素同样保留原始 JSON（节点 inbound 逐字节保留，§7"原样写入"）。
type fullConfig map[string]json.RawMessage

func (fc fullConfig) clone() fullConfig {
	nc := make(fullConfig, len(fc)+1)
	for k, v := range fc {
		nc[k] = v
	}
	return nc
}

func (fc fullConfig) inbounds() []json.RawMessage {
	raw, ok := fc["inbounds"]
	if !ok {
		return nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil
	}
	return list
}

func (fc fullConfig) setInbounds(list []json.RawMessage) {
	b, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	fc["inbounds"] = b
}

func inboundTag(raw json.RawMessage) string {
	var p struct {
		Tag string `json:"tag"`
	}
	json.Unmarshal(raw, &p)
	return p.Tag
}

// upsertInbound 返回"移除同 tag 后追加新 inbound"的候选配置（不修改原配置）。
func (fc fullConfig) upsertInbound(tag string, inbound json.RawMessage) fullConfig {
	out := make([]json.RawMessage, 0, len(fc.inbounds())+1)
	for _, raw := range fc.inbounds() {
		if inboundTag(raw) != tag {
			out = append(out, raw)
		}
	}
	out = append(out, inbound)
	nc := fc.clone()
	nc.setInbounds(out)
	return nc
}

// removeInbound 返回移除指定 tag 的候选配置；第二个返回值表示该 tag 是否存在。
func (fc fullConfig) removeInbound(tag string) (fullConfig, bool) {
	existed := false
	out := make([]json.RawMessage, 0, len(fc.inbounds()))
	for _, raw := range fc.inbounds() {
		if inboundTag(raw) == tag {
			existed = true
			continue
		}
		out = append(out, raw)
	}
	if !existed {
		return fc, false
	}
	nc := fc.clone()
	nc.setInbounds(out)
	return nc, true
}

// mutateClients 在节点 inbound（tag 前缀 node_）的用户列表中增删一个用户（§8/§16）：
// vless/vmess/trojan/ss 操作 settings.clients，socks/http 操作 settings.accounts，
// dokodemo 无用户概念跳过。params 按 tag 提供各协议条目构造参数，
// 仅处理其中列出的 tag（§16 增量扇出；agent 入口已拒绝缺省 Nodes 的载荷）。
func (fc fullConfig) mutateClients(uuid string, add bool, params map[string]shared.UserNodeParams) (fullConfig, []string) {
	var changed []string
	out := make([]json.RawMessage, 0, len(fc.inbounds()))
	for _, raw := range fc.inbounds() {
		tag := inboundTag(raw)
		if !strings.HasPrefix(tag, "node_") {
			out = append(out, raw)
			continue
		}
		p, ok := params[tag]
		if !ok {
			out = append(out, raw) // §16：未列出的节点不受影响
			continue
		}
		newRaw, changedOne := mutateInboundUsers(raw, uuid, add, p)
		if changedOne {
			changed = append(changed, tag)
			raw = newRaw
		}
		out = append(out, raw)
	}
	if len(changed) == 0 {
		return fc, nil
	}
	nc := fc.clone()
	nc.setInbounds(out)
	return nc, changed
}

// mutateInboundUsers 修改单个 inbound 的用户列表（clients 或 accounts）；返回是否发生变更。
func mutateInboundUsers(raw json.RawMessage, uuid string, add bool, p shared.UserNodeParams) (json.RawMessage, bool) {
	var ib map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ib); err != nil {
		return raw, false
	}
	var protocol string
	_ = json.Unmarshal(ib["protocol"], &protocol)
	if protocol == "" || protocol == shared.ProtocolDokodemo {
		return raw, false
	}
	if p.Protocol == "" {
		p.Protocol = protocol // payload 未携带时以 inbound 自身协议为准（兜底）
	}
	key := "clients"
	if protocol == shared.ProtocolSocks || protocol == shared.ProtocolHTTP {
		key = "accounts"
	}
	settingsRaw, ok := ib["settings"]
	if !ok {
		return raw, false
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		return raw, false
	}
	var list []json.RawMessage
	if c, ok := settings[key]; ok {
		if err := json.Unmarshal(c, &list); err != nil {
			return raw, false
		}
	}
	list, changed := mutateUserList(list, p, settings, uuid, add)
	if !changed {
		return raw, false
	}
	c, err := json.Marshal(list)
	if err != nil {
		return raw, false
	}
	settings[key] = c
	s, err := json.Marshal(settings)
	if err != nil {
		return raw, false
	}
	ib["settings"] = s
	out, err := json.Marshal(ib)
	if err != nil {
		return raw, false
	}
	return out, true
}

// mutateUserList 增删用户列表中该 uuid 的条目（幂等）。
// 匹配键：clients 条目按 email（与 RemoveUserOperation 一致），accounts 条目按 user。
func mutateUserList(list []json.RawMessage, p shared.UserNodeParams, settings map[string]json.RawMessage, uuid string, add bool) ([]json.RawMessage, bool) {
	idx := -1
	for i, c := range list {
		var probe struct {
			Email string `json:"email"`
			User  string `json:"user"`
		}
		if json.Unmarshal(c, &probe) == nil && (probe.Email == uuid || probe.User == uuid) {
			idx = i
			break
		}
	}
	if add {
		if idx >= 0 {
			return list, false
		}
		method := p.Method
		if method == "" && p.Protocol == shared.ProtocolShadowsocks {
			_ = json.Unmarshal(settings["method"], &method) // 兜底取 inbound 顶层 method
		}
		entry, err := json.Marshal(clientEntry(p.Protocol, p.Flow, method, uuid))
		if err != nil {
			return list, false
		}
		return append(list, entry), true
	}
	if idx < 0 {
		return list, false
	}
	return append(list[:idx], list[idx+1:]...), true
}
