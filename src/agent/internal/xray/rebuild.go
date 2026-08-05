package xray

import (
	"encoding/json"
	"strings"

	"lattix/shared"
)

// extractPrevInbound 提取备份 inbound 的当前生效值（重建时复用，保证客户端不断连）：
// 监听端口、Reality 私钥（streamSettings.realitySettings.privateKey）、
// VLESS Encryption decryption（settings.decryption）。
func extractPrevInbound(raw json.RawMessage) (port int, privateKey, decryption string) {
	var ib struct {
		Port     int `json:"port"`
		Settings struct {
			Decryption string `json:"decryption"`
		} `json:"settings"`
		StreamSettings struct {
			RealitySettings struct {
				PrivateKey string `json:"privateKey"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	}
	if err := json.Unmarshal(raw, &ib); err != nil {
		return 0, "", ""
	}
	return ib.Port, ib.StreamSettings.RealitySettings.PrivateKey, ib.Settings.Decryption
}

// rebuildInbound 以保留模式重渲染一个 node inbound（重建专用，§docs/rebuild-xray-config-design.md）：
// 端口/私钥/decryption 优先复用 prev（备份）中的当前生效值——占位符预替换后
// fillTemplate 不再重新生成；prev 缺失时回退 fillTemplate 生成路径。
// minClientVer 由 fillTemplate 内 pinRealityMinClientVer 兜底注入。
func (m *Manager) rebuildInbound(tag string, vc shared.VirtualConfig, userUUIDs, destCandidates []string, portCandidates []int, prev json.RawMessage) (json.RawMessage, *shared.RealizedConfig, error) {
	port, privateKey, decryption := extractPrevInbound(prev)
	t := string(vc.Template)
	if privateKey != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderRealityPrivateKey, privateKey)
	}
	if decryption != "" {
		t = strings.ReplaceAll(t, shared.PlaceholderVLessDecryption, decryption)
	}
	if port == 0 {
		var err error
		if port, err = m.pickPort(vc.Port, portCandidates); err != nil {
			return nil, nil, err
		}
	}
	rebuilt := vc
	rebuilt.Template = json.RawMessage(t)
	return m.fillTemplate(port, tag, rebuilt, userUUIDs, destCandidates, portCandidates)
}
