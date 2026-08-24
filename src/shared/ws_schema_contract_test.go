package shared

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// 本文件把 docs/ws-protocol.schema.json（标注为"参考性描述"，无运行时/CI 校验）
// 中的关键约定固化为针对性断言：schema 与 Go 序列化再次漂移时，这些测试应变红。
// 不引入 JSON Schema 依赖，只做 schema 已描述的关键类型的手工断言，不求全覆盖。
//
// go test 的工作目录是包目录（src/shared），故用相对路径回退到仓库根 docs/。
const wsSchemaPath = "../../docs/ws-protocol.schema.json"

func loadWSSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(wsSchemaPath)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", wsSchemaPath, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("解析 %s 失败: %v", wsSchemaPath, err)
	}
	return schema
}

// propMatches 判断 schema if 分支中某字段是否匹配 want：
// const 精确匹配；enum 包含匹配；字段缺失时仅匹配空 want。
func propMatches(props map[string]any, name, want string) bool {
	prop, ok := props[name].(map[string]any)
	if !ok {
		return want == ""
	}
	if s, ok := prop["const"].(string); ok {
		return s == want
	}
	if enum, ok := prop["enum"].([]any); ok {
		for _, item := range enum {
			if item == want {
				return true
			}
		}
		return false
	}
	return want == ""
}

// schemaBranch 在 schema.allOf 中定位 if 条件匹配（kind/type/code）的分支并返回其 then。
func schemaBranch(t *testing.T, schema map[string]any, kind, msgType, code string) map[string]any {
	t.Helper()
	allOf, ok := schema["allOf"].([]any)
	if !ok {
		t.Fatal("schema.allOf 缺失或不是数组")
	}
	for _, item := range allOf {
		branch, _ := item.(map[string]any)
		ifs, _ := branch["if"].(map[string]any)
		props, _ := ifs["properties"].(map[string]any)
		if !propMatches(props, "kind", kind) || !propMatches(props, "type", msgType) || !propMatches(props, "code", code) {
			continue
		}
		if then, ok := branch["then"].(map[string]any); ok {
			return then
		}
		t.Fatalf("schema 分支 %s/%s (code=%q) 缺少 then", kind, msgType, code)
	}
	t.Fatalf("schema 缺少 %s/%s (code=%q) 分支", kind, msgType, code)
	return nil
}

// dataRule 返回指定 kind/type（code 可选）分支 then.properties.data 的约束。
func dataRule(t *testing.T, schema map[string]any, kind, msgType, code string) map[string]any {
	t.Helper()
	then := schemaBranch(t, schema, kind, msgType, code)
	props, _ := then["properties"].(map[string]any)
	data, ok := props["data"].(map[string]any)
	if !ok {
		t.Fatalf("schema 分支 %s/%s (code=%q) 缺少 data 约束", kind, msgType, code)
	}
	return data
}

// defOf 返回 schema.$defs[name]。
func defOf(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	defs, _ := schema["$defs"].(map[string]any)
	def, ok := defs[name].(map[string]any)
	if !ok {
		t.Fatalf("schema.$defs 缺少 %q", name)
	}
	return def
}

// propertyOf 返回 rule.properties[name]。
func propertyOf(t *testing.T, ctx string, rule map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := rule["properties"].(map[string]any)
	prop, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("%s: schema 缺少 property %q", ctx, name)
	}
	return prop
}

// wireFields 返回 Go 结构体的全部 JSON 字段名（json tag 名，忽略 omitempty）。
func wireFields(t *testing.T, sample any) map[string]bool {
	t.Helper()
	typ := reflect.TypeOf(sample)
	if typ == nil || typ.Kind() != reflect.Struct {
		t.Fatalf("%T 不是结构体", sample)
	}
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		fields[name] = true
	}
	return fields
}

// stringSet 把 schema 中的字符串数组（required/enum）转为集合。
func stringSet(t *testing.T, ctx string, v any) map[string]bool {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("%s: 期望字符串数组，实际缺失或类型错误", ctx)
	}
	set := make(map[string]bool, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("%s: 数组含非字符串项 %v", ctx, item)
		}
		set[s] = true
	}
	return set
}

// assertRequired 断言 rule.required 恰为 want，且每个字段都存在于 wire（Go 结构体的 JSON 字段）。
func assertRequired(t *testing.T, ctx string, rule map[string]any, want []string, wire map[string]bool) {
	t.Helper()
	got := stringSet(t, ctx+".required", rule["required"])
	if len(got) != len(want) {
		t.Fatalf("%s: schema required = %v, 期望 %v", ctx, got, want)
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s: schema required 缺少 %q（现有 %v）", ctx, name, got)
		}
		if !wire[name] {
			t.Fatalf("%s: schema 要求 %q，但 Go 结构体无此 JSON 字段", ctx, name)
		}
	}
}

// assertWireCovered 断言 Go wire 字段 ⊆ schema properties：
// Go 侧新增/重命名字段而不同步 schema 时变红。
func assertWireCovered(t *testing.T, ctx string, rule map[string]any, wire map[string]bool) {
	t.Helper()
	props, ok := rule["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: schema 缺少 properties", ctx)
	}
	for name := range wire {
		if _, ok := props[name]; !ok {
			t.Fatalf("%s: Go 字段 %q 未登记在 schema properties", ctx, name)
		}
	}
}

// assertClosed 断言对象为封闭对象（additionalProperties:false）。
func assertClosed(t *testing.T, ctx string, rule map[string]any) {
	t.Helper()
	closed, ok := rule["additionalProperties"].(bool)
	if !ok || closed {
		t.Fatalf("%s: 期望 additionalProperties:false（封闭对象约定）", ctx)
	}
}

// assertNullable 断言字段的 schema type 允许 null（union 中含 "null"）。
func assertNullable(t *testing.T, ctx string, rule map[string]any, name string) {
	t.Helper()
	prop := propertyOf(t, ctx, rule, name)
	types, ok := prop["type"].([]any)
	if !ok {
		t.Fatalf("%s: %s.type 不是 union，不允许 null", ctx, name)
	}
	for _, item := range types {
		if item == "null" {
			return
		}
	}
	t.Fatalf("%s: %s 的 schema type %v 不允许 null", ctx, name, types)
}

// assertEnum 断言 prop.enum 恰为 want（用于与 Go 常量比对）。
func assertEnum(t *testing.T, ctx string, prop map[string]any, want []string) {
	t.Helper()
	got := stringSet(t, ctx+".enum", prop["enum"])
	if len(got) != len(want) {
		t.Fatalf("%s: schema enum = %v, 期望 %v", ctx, got, want)
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("%s: schema enum 缺少 %q（现有 %v）", ctx, name, got)
		}
	}
}

func TestWSSchemaEnvelopeContract(t *testing.T) {
	schema := loadWSSchema(t)
	wire := wireFields(t, Envelope{})

	// 顶层 required：信封五元组必须在 Go Envelope 中存在。
	assertRequired(t, "envelope", schema, []string{"kind", "type", "request_id", "trace_id", "data"}, wire)

	props, _ := schema["properties"].(map[string]any)

	// kind 枚举与 Kind* 常量一一对应。
	kindProp, _ := props["kind"].(map[string]any)
	assertEnum(t, "envelope.kind", kindProp, []string{KindRequest, KindResponse, KindEvent})

	// type 的 domain.action 格式约束对所有 Type* 常量生效（新增常量时同步此列表）。
	typeProp, _ := props["type"].(map[string]any)
	pattern, _ := typeProp["pattern"].(string)
	typeRe, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("envelope.type pattern 无法编译: %v", err)
	}
	types := []string{
		TypeSessionOpen, TypeSessionReady, TypeCredentialCommit,
		TypeLifecycleChanged, TypeSettingsSync, TypeSettingsChanged,
		TypeServerSettingsSync, TypeServerSettingsChanged,
		TypeApplyNode, TypeRemoveNode, TypeAddUser, TypeRemoveUser,
		TypeUninstall, TypeUpgradeXray, TypeUpgradeAgent,
		TypeTelemetry, TypeDriftReport,
		TypeApplyChainHop, TypeRemoveChainHop,
		TypeApplySharedEndpoint, TypeRemoveSharedEndpoint,
		TypeCleanupXray, TypeRebuildXray,
	}
	for _, typ := range types {
		if !typeRe.MatchString(typ) {
			t.Fatalf("Type 常量 %q 不满足 schema type pattern %q", typ, pattern)
		}
	}

	// request_id/trace_id 的 32 位小写 hex 约束与 NewMessageID 的产物一致。
	id := NewMessageID()
	for _, name := range []string{"request_id", "trace_id"} {
		prop, _ := props[name].(map[string]any)
		p, _ := prop["pattern"].(string)
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("envelope.%s pattern 无法编译: %v", name, err)
		}
		if !re.MatchString(id) {
			t.Fatalf("NewMessageID() = %q 不满足 schema %s pattern %q", id, name, p)
		}
	}

	// 信封不变式（与 TestEnvelopeMarshalSeparatesRequestAndResponseFields 互为两端）：
	// response 必带 code/message；request/event 不得携带。
	responseThen := schemaBranch(t, schema, KindResponse, "", "")
	if got := stringSet(t, "envelope response 分支 required", responseThen["required"]); !got["code"] || !got["message"] {
		t.Fatalf("schema response 分支未强制 code/message: %v", got)
	}
	requestThen := schemaBranch(t, schema, KindRequest, "", "")
	if _, ok := requestThen["not"]; !ok {
		t.Fatal("schema request/event 分支缺少 not（禁止 code/message）约束")
	}
}

func TestWSSchemaSharedEndpointApplyContract(t *testing.T) {
	schema := loadWSSchema(t)
	ctx := "shared-endpoint.apply request data"
	data := dataRule(t, schema, KindRequest, TypeApplySharedEndpoint, "")
	wire := wireFields(t, ApplySharedEndpointPayload{})

	assertRequired(t, ctx, data, []string{"endpoint_id", "config", "clients", "routes"}, wire)
	assertWireCovered(t, ctx, data, wire)
	assertClosed(t, ctx, data)

	// clients/routes 无 omitempty，空切片序列化为 null；schema 必须允许 null。
	// 这是第一期对齐过的漂移点，任一侧回退都应变红。
	assertNullable(t, ctx, data, "clients")
	assertNullable(t, ctx, data, "routes")
	raw, err := json.Marshal(ApplySharedEndpointPayload{EndpointID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"clients":null`) || !strings.Contains(string(raw), `"routes":null`) {
		t.Fatalf("零值 ApplySharedEndpointPayload 的 clients/routes 应序列化为 null: %s", raw)
	}
}

func TestWSSchemaChainHopApplyContract(t *testing.T) {
	schema := loadWSSchema(t)
	ctx := "chain-hop.apply request data"
	data := dataRule(t, schema, KindRequest, TypeApplyChainHop, "")
	wire := wireFields(t, ApplyChainHopPayload{})

	assertRequired(t, ctx, data, []string{"chain_id", "hop_id", "kind"}, wire)

	// revision_id 为 omitempty：协议上不属于 required，但运行期 dispatch 总会赋值
	//（backend/internal/dispatch/chain.go 三处 ApplyChainHopPayload 构造点均设置 RevisionID）。
	// schema 必须保持"非 required + 有运行期赋值说明"，任一侧改动都应变红。
	if stringSet(t, ctx+".required", data["required"])["revision_id"] {
		t.Fatalf("%s: revision_id 不应是 required（Go 侧为 omitempty）", ctx)
	}
	if !wire["revision_id"] {
		t.Fatalf("%s: Go 结构体缺少 revision_id 字段", ctx)
	}
	revision := propertyOf(t, ctx, data, "revision_id")
	if desc, _ := revision["description"].(string); !strings.Contains(desc, "运行期") {
		t.Fatalf("%s: revision_id 缺少运行期赋值的约定说明: %q", ctx, desc)
	}

	// kind 枚举与 HopKind* 常量一一对应。
	assertEnum(t, ctx, propertyOf(t, ctx, data, "kind"), []string{HopKindPortal, HopKindBridge, HopKindForward})

	// omitempty 线行为：零值 RevisionID 不出现在报文中，赋值后出现。
	raw, err := json.Marshal(ApplyChainHopPayload{ChainID: 1, HopID: 2, Kind: HopKindPortal})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "revision_id") {
		t.Fatalf("RevisionID 为 0 时不应出现在报文中: %s", raw)
	}
	raw, err = json.Marshal(ApplyChainHopPayload{ChainID: 1, RevisionID: 7, HopID: 2, Kind: HopKindPortal})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"revision_id":7`) {
		t.Fatalf("RevisionID 赋值后应出现在报文中: %s", raw)
	}
}

func TestWSSchemaSessionContract(t *testing.T) {
	schema := loadWSSchema(t)

	ctx := "agent.session.open request data"
	openReq := dataRule(t, schema, KindRequest, TypeSessionOpen, "")
	wire := wireFields(t, SessionOpenPayload{})
	assertRequired(t, ctx, openReq, []string{"protocol_version", "agent_version", "xray_version", "xray_running"}, wire)
	assertWireCovered(t, ctx, openReq, wire)
	assertClosed(t, ctx, openReq)
	// protocol_version 固定为 1（Go 侧在 agent/cmd/agent/main.go 硬编码 1）。
	if v, ok := propertyOf(t, ctx, openReq, "protocol_version")["const"].(float64); !ok || int(v) != 1 {
		t.Fatalf("%s: protocol_version const 应为 1, got %v", ctx, v)
	}

	ctx = "agent.session.open response data"
	openResp := dataRule(t, schema, KindResponse, TypeSessionOpen, CodeOK)
	wire = wireFields(t, SessionOpenResult{})
	assertRequired(t, ctx, openResp, []string{"server_id", "session_id", "session_kind", "panel_state"}, wire)
	assertWireCovered(t, ctx, openResp, wire)
	assertClosed(t, ctx, openResp)
	assertEnum(t, ctx, propertyOf(t, ctx, openResp, "session_kind"), []string{SessionKindInitial, SessionKindReconnect})

	ctx = "agent.session.ready request data"
	ready := dataRule(t, schema, KindRequest, TypeSessionReady, "")
	wire = wireFields(t, SessionReadyPayload{})
	assertRequired(t, ctx, ready, []string{"session_id", "lifecycle"}, wire)
	assertWireCovered(t, ctx, ready, wire)
	assertClosed(t, ctx, ready)

	ctx = "agent.credential.commit request data"
	commit := dataRule(t, schema, KindRequest, TypeCredentialCommit, "")
	wire = wireFields(t, CredentialCommitPayload{})
	assertRequired(t, ctx, commit, []string{"exchange_id"}, wire)
	assertClosed(t, ctx, commit)

	ctx = "panel.lifecycle.changed request data"
	changed := dataRule(t, schema, KindRequest, TypeLifecycleChanged, "")
	wire = wireFields(t, LifecycleChangedPayload{})
	assertRequired(t, ctx, changed, []string{"panel_state"}, wire)
	assertClosed(t, ctx, changed)
}

func TestWSSchemaPanelLifecycleDefContract(t *testing.T) {
	schema := loadWSSchema(t)

	ctx := "$defs.panelLifecycle"
	lc := defOf(t, schema, "panelLifecycle")
	wire := wireFields(t, PanelLifecycleSnapshot{})
	assertRequired(t, ctx, lc,
		[]string{"panel_instance_id", "state", "epoch", "revision", "entered_at", "retry_policy", "latency_resume_window_ms"}, wire)
	assertWireCovered(t, ctx, lc, wire)
	assertClosed(t, ctx, lc)
	assertEnum(t, ctx, propertyOf(t, ctx, lc, "state"),
		[]string{PanelStateStartup, PanelStateActive, PanelStateUpdating, PanelStateFaulted})

	retry := propertyOf(t, ctx, lc, "retry_policy")
	assertRequired(t, ctx+".retry_policy", retry, []string{"min_ms", "max_ms"}, wireFields(t, RetryPolicy{}))

	version := defOf(t, schema, "lifecycleVersion")
	assertRequired(t, "$defs.lifecycleVersion", version, []string{"epoch", "revision"}, wireFields(t, LifecycleVersion{}))
}

func TestWSSchemaSettingsContract(t *testing.T) {
	schema := loadWSSchema(t)

	ctx := "agent.settings.sync request data"
	syncReq := dataRule(t, schema, KindRequest, TypeSettingsSync, "")
	wire := wireFields(t, SettingsSyncPayload{})
	assertRequired(t, ctx, syncReq, []string{"panel_instance_id", "applied_revision"}, wire)
	assertWireCovered(t, ctx, syncReq, wire)
	assertClosed(t, ctx, syncReq)

	ctx = "agent.settings.sync response data"
	syncResp := dataRule(t, schema, KindResponse, TypeSettingsSync, "")
	wire = wireFields(t, AgentSettingsSyncResult{})
	assertRequired(t, ctx, syncResp, []string{"changed"}, wire)
	assertWireCovered(t, ctx, syncResp, wire)
	assertClosed(t, ctx, syncResp)

	ctx = "agent.settings.changed event data"
	changed := dataRule(t, schema, KindEvent, TypeSettingsChanged, "")
	assertRequired(t, ctx, changed, []string{"revision"}, wireFields(t, AgentSettingsChangedPayload{}))
	assertClosed(t, ctx, changed)

	// $defs.agentSettings / settingsDocument 与配置结构体对齐。
	ctx = "$defs.agentSettings"
	settings := defOf(t, schema, "agentSettings")
	wire = wireFields(t, AgentSettings{})
	assertRequired(t, ctx, settings, []string{"revision", "reconnect", "telemetry", "drift_detection"}, wire)
	assertClosed(t, ctx, settings)

	reconnect := propertyOf(t, ctx, settings, "reconnect")
	assertRequired(t, ctx+".reconnect", reconnect, []string{"mode", "max_retries"}, wireFields(t, AgentReconnectSettings{}))
	assertEnum(t, ctx+".reconnect", propertyOf(t, ctx+".reconnect", reconnect, "mode"),
		[]string{ReconnectModeInfinite, ReconnectModeLimited})
	for _, name := range []string{"telemetry", "drift_detection"} {
		interval := propertyOf(t, ctx, settings, name)
		assertRequired(t, ctx+"."+name, interval, []string{"interval_seconds"}, wireFields(t, AgentIntervalSettings{}))
	}

	ctx = "$defs.settingsDocument"
	doc := defOf(t, schema, "settingsDocument")
	wire = wireFields(t, AgentSettingsDocument{})
	assertRequired(t, ctx, doc, []string{"schema_version", "panel", "agent"}, wire)
	assertClosed(t, ctx, doc)
	if v, ok := propertyOf(t, ctx, doc, "schema_version")["const"].(float64); !ok || int(v) != AgentSettingsSchemaVersion {
		t.Fatalf("%s: schema_version const 应为 %d, got %v", ctx, AgentSettingsSchemaVersion, v)
	}
	panel := propertyOf(t, ctx, doc, "panel")
	assertRequired(t, ctx+".panel", panel,
		[]string{"instance_id", "version", "public_url", "ws_url"}, wireFields(t, PanelMetadata{}))
}

func TestWSSchemaTelemetryContract(t *testing.T) {
	schema := loadWSSchema(t)
	ctx := "telemetry.report event data"
	data := dataRule(t, schema, KindEvent, TypeTelemetry, "")
	wire := wireFields(t, TelemetryPayload{})
	assertRequired(t, ctx, data, []string{"xray_version", "xray_running", "online_users"}, wire)
	assertWireCovered(t, ctx, data, wire)

	// online_users 允许显式 null：nil（查询失败/不支持）与 []（全员离线）语义不同，
	// 与 TestTelemetryPayloadAlwaysCarriesOnlineUsers 的序列化行为互为协议两端。
	oneOf, ok := propertyOf(t, ctx, data, "online_users")["oneOf"].([]any)
	if !ok {
		t.Fatalf("%s: online_users 缺少 oneOf", ctx)
	}
	nullOK := false
	for _, branch := range oneOf {
		if b, _ := branch.(map[string]any); b["type"] == "null" {
			nullOK = true
		}
	}
	if !nullOK {
		t.Fatalf("%s: online_users 不允许 null，与 TelemetryPayload 的显式 null 约定漂移", ctx)
	}

	// host 指标：schema required 必须都在 HostMetrics 中，且 Go 字段全部已登记。
	host := propertyOf(t, ctx, data, "host")
	hostWire := wireFields(t, HostMetrics{})
	assertRequired(t, ctx+".host", host,
		[]string{"load1", "load5", "load15", "mem_total", "mem_used", "disk_total", "disk_used",
			"network_tx_bytes", "network_rx_bytes", "uptime_seconds"}, hostWire)
	assertWireCovered(t, ctx+".host", host, hostWire)

	// trafficCounter：up/down 必填；node_id/endpoint_id/hop_id/user 四选一标识都须在 schema 登记。
	counter := defOf(t, schema, "trafficCounter")
	assertRequired(t, "$defs.trafficCounter", counter, []string{"up", "down"}, wireFields(t, TrafficCounter{}))
	for _, name := range []string{"node_id", "endpoint_id", "hop_id", "user"} {
		propertyOf(t, "$defs.trafficCounter", counter, name)
	}
}

func TestWSSchemaCleanupContract(t *testing.T) {
	schema := loadWSSchema(t)

	ctx := "xray.cleanup request data"
	req := dataRule(t, schema, KindRequest, TypeCleanupXray, "")
	wire := wireFields(t, CleanupXrayPayload{})
	assertRequired(t, ctx, req, []string{"dry_run"}, wire)
	assertWireCovered(t, ctx, req, wire)

	ctx = "xray.cleanup response data"
	resp := dataRule(t, schema, KindResponse, TypeCleanupXray, "")
	assertWireCovered(t, ctx, resp, wireFields(t, CleanupXrayResult{}))
}
