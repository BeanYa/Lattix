package shared

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestRebuildXrayResultJSONArrays 验证回执 JSON 契约：数组字段必须是 [] 而非 null
// （前端直接读 .length，null 会崩溃，与 CleanupXrayResult 同约定）。
func TestRebuildXrayResultJSONArrays(t *testing.T) {
	result := RebuildXrayResult{}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"rebuilt_inbounds":[],"rebuilt_pieces":[],"rolled_back":false}` {
		t.Fatalf("回执 JSON = %s", b)
	}
}

// TestApplyResultPayloadCarriesRebuild 验证回执 data 挂载点：rebuild 字段可解出。
func TestApplyResultPayloadCarriesRebuild(t *testing.T) {
	b, err := json.Marshal(ApplyResultPayload{
		Rebuild: &RebuildXrayResult{RebuiltInbounds: []RebuiltInbound{{Tag: "node_1", Port: 10001, Kind: "vless"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded ApplyResultPayload
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Rebuild == nil || len(decoded.Rebuild.RebuiltInbounds) != 1 ||
		decoded.Rebuild.RebuiltInbounds[0].Tag != "node_1" || decoded.Rebuild.RebuiltInbounds[0].Port != 10001 ||
		decoded.Rebuild.RebuiltInbounds[0].Kind != "vless" {
		t.Fatalf("rebuild 回执 = %+v", decoded.Rebuild)
	}
}

// TestRebuildXrayPayloadKeys 验证面板下发载荷的 JSON 键名（agent 侧解析依赖）。
func TestRebuildXrayPayloadKeys(t *testing.T) {
	b, err := json.Marshal(RebuildXrayPayload{
		Nodes:               []ApplyNodePayload{{NodeID: 7, UserUUIDs: []string{"u1"}}},
		ExpectedInboundTags: []string{"node_7"},
		ExpectedPieces:      []string{"forward/3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"nodes"`, `"expected_inbound_tags"`, `"expected_pieces"`, `"node_id"`, `"user_uuids"`} {
		if !json.Valid(b) || !bytes.Contains(b, []byte(key)) {
			t.Fatalf("载荷 %s 缺少字段 %s", b, key)
		}
	}
}
