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

// mutateClients 在所有节点 inbound（tag 前缀 node_）的 settings.clients 中
// 增删一个用户（§8），返回候选配置与受影响的 tag 列表。
func (fc fullConfig) mutateClients(uuid string, add bool) (fullConfig, []string) {
	var changed []string
	out := make([]json.RawMessage, 0, len(fc.inbounds()))
	for _, raw := range fc.inbounds() {
		tag := inboundTag(raw)
		if !strings.HasPrefix(tag, "node_") {
			out = append(out, raw)
			continue
		}
		newRaw, ok := mutateInboundClients(raw, uuid, add)
		if ok {
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

// mutateInboundClients 修改单个 inbound 的 settings.clients；返回是否发生变更。
func mutateInboundClients(raw json.RawMessage, uuid string, add bool) (json.RawMessage, bool) {
	var ib map[string]json.RawMessage
	if err := json.Unmarshal(raw, &ib); err != nil {
		return raw, false
	}
	settingsRaw, ok := ib["settings"]
	if !ok {
		return raw, false
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		return raw, false
	}
	var clients []json.RawMessage
	if c, ok := settings["clients"]; ok {
		if err := json.Unmarshal(c, &clients); err != nil {
			return raw, false
		}
	}
	clients, changed := mutateClientList(clients, uuid, add)
	if !changed {
		return raw, false
	}
	c, err := json.Marshal(clients)
	if err != nil {
		return raw, false
	}
	settings["clients"] = c
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

// mutateClientList 增删 clients 中 id 为 uuid 的条目（幂等）。
// VLESS client 的 email 固定取 uuid（RemoveUserOperation 按 email 匹配，§6）。
func mutateClientList(clients []json.RawMessage, uuid string, add bool) ([]json.RawMessage, bool) {
	idx := -1
	for i, c := range clients {
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(c, &p) == nil && p.ID == uuid {
			idx = i
			break
		}
	}
	if add {
		if idx >= 0 {
			return clients, false
		}
		entry, err := json.Marshal(map[string]string{"id": uuid, "flow": shared.FlowVision, "email": uuid})
		if err != nil {
			return clients, false
		}
		return append(clients, entry), true
	}
	if idx < 0 {
		return clients, false
	}
	return append(clients[:idx], clients[idx+1:]...), true
}
