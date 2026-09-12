package sub

import (
	"encoding/json"

	"lattix/shared"
)

// vmessCipher 读取面板侧 VirtualConfig.Cipher（订阅提示；空/损坏回退 auto）。
func vmessCipher(configTemplate json.RawMessage) string {
	var vc shared.VirtualConfig
	if err := json.Unmarshal(configTemplate, &vc); err == nil && vc.Cipher != "" {
		return vc.Cipher
	}
	return shared.VMessCipherAuto
}
