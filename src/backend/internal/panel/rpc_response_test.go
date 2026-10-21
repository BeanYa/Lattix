package panel

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/shared"
)

// 固定 request/trace ID：与生产中间件一致（32 位小写十六进制），便于逐字节断言。
const (
	rpcTestRequestID = "0123456789abcdef0123456789abcdef"
	rpcTestTraceID   = "fedcba9876543210fedcba9876543210"
)

type rpcRecordedOutcome struct {
	code    string
	message string
}

// rpcRecordingWriter 模拟生产请求日志中间件包装器：提供固定消息 ID 并记录
// SetRPCOutcome 的调用顺序（后调用覆盖前者）。
type rpcRecordingWriter struct {
	*httptest.ResponseRecorder
	outcomes []rpcRecordedOutcome
}

func newRPCRecordingWriter() *rpcRecordingWriter {
	return &rpcRecordingWriter{ResponseRecorder: httptest.NewRecorder()}
}

func (w *rpcRecordingWriter) SetRPCOutcome(code, safeMessage string) {
	w.outcomes = append(w.outcomes, rpcRecordedOutcome{code: code, message: safeMessage})
}

func (w *rpcRecordingWriter) RPCIDs() (string, string) { return rpcTestRequestID, rpcTestTraceID }

func (w *rpcRecordingWriter) ObserveID() string { return "" }

// jsonString 返回字符串在 JSON 中的字面量（含引号），用于按原始字节断言字段值。
func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// decodeRPCJSON 断言响应体是完整合法 JSON（且保留结尾换行），按字段返回原始值；
// 用 map 保留 data 键存在性（前端要求 envelope 必须带 data）。
func decodeRPCJSON(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	if !json.Valid(body) {
		t.Fatalf("响应体不是合法 JSON（截断?）: %q", body)
	}
	if !bytes.HasSuffix(body, []byte("\n")) {
		t.Fatalf("响应体缺少结尾换行: %q", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("解码 envelope 字段: %v, body=%q", err, body)
	}
	return fields
}

func assertRPCField(t *testing.T, fields map[string]json.RawMessage, key, wantJSON string) {
	t.Helper()
	raw, ok := fields[key]
	if !ok {
		t.Fatalf("envelope 缺少字段 %q: %v", key, fields)
	}
	if string(raw) != wantJSON {
		t.Fatalf("字段 %q = %s, want %s", key, raw, wantJSON)
	}
}

// TestWriteRPCEncodingFailureReturnsCompleteProtocolEnvelope 复现原始 bug：handler
// 数据不可 marshal 时，writeRPC 必须写出完整合法的 HTTP_500 协议错误信封，而不是
// 先写字头再截断 JSON；同时 rpc outcome 必须记录失败（覆盖先前的成功 code）。
func TestWriteRPCEncodingFailureReturnsCompleteProtocolEnvelope(t *testing.T) {
	cases := []struct {
		name string
		data any
	}{
		{name: "empty json.RawMessage", data: json.RawMessage("")},
		{name: "truncated json.RawMessage", data: json.RawMessage("{\"chain\":")},
		{name: "NaN float", data: math.NaN()},
		{name: "nested invalid json.RawMessage", data: map[string]json.RawMessage{"payload": json.RawMessage("{")}},
		{name: "unsupported channel type", data: make(chan int)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := newRPCRecordingWriter()
			writeRPC(writer, shared.CodeOK, "", tc.data)

			if writer.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", writer.Code, http.StatusInternalServerError)
			}
			if contentType := writer.Header().Get("Content-Type"); contentType != "application/problem+json; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want application/problem+json", contentType)
			}
			fields := decodeRPCJSON(t, writer.Body.Bytes())
			assertRPCField(t, fields, "code", jsonString("HTTP_500"))
			assertRPCField(t, fields, "message", jsonString("response encoding failed"))
			assertRPCField(t, fields, "data", "null")
			assertRPCField(t, fields, "request_id", jsonString(rpcTestRequestID))
			assertRPCField(t, fields, "trace_id", jsonString(rpcTestTraceID))
			// 失败路径不得残留成功信封片段。
			if bytes.Contains(writer.Body.Bytes(), []byte(jsonString(shared.CodeOK))) {
				t.Fatalf("响应体残留成功信封: %q", writer.Body.Bytes())
			}
			// outcome：先按调用方 code 记录，再被失败 code 覆盖（日志据此记 error）。
			if len(writer.outcomes) != 2 {
				t.Fatalf("outcome 调用次数 = %d, want 2: %+v", len(writer.outcomes), writer.outcomes)
			}
			if writer.outcomes[0].code != shared.CodeOK {
				t.Fatalf("首次 outcome = %+v, want code %q", writer.outcomes[0], shared.CodeOK)
			}
			last := writer.outcomes[len(writer.outcomes)-1]
			if last.code != "HTTP_500" || last.message != "response encoding failed" {
				t.Fatalf("最终 outcome = %+v, want HTTP_500/response encoding failed", last)
			}
		})
	}
}

// TestWriteRPCEncodingSuccessMatchesLegacyEncoderOutput 固定成功路径字节格式：
// 与原 json.NewEncoder(...).Encode 输出逐字节一致（含结尾换行、HTML 转义）。
func TestWriteRPCEncodingSuccessMatchesLegacyEncoderOutput(t *testing.T) {
	writer := newRPCRecordingWriter()
	data := map[string]any{"chain_id": 7, "name": "香港-01", "note": "a<b"}
	writeRPC(writer, shared.CodeOK, "", data)

	if writer.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", writer.Code, http.StatusOK)
	}
	if contentType := writer.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	expected, err := json.Marshal(rpcResponse{
		Code: shared.CodeOK, Data: data, RequestID: rpcTestRequestID, TraceID: rpcTestTraceID,
	})
	if err != nil {
		t.Fatalf("构造期望响应: %v", err)
	}
	expected = append(expected, '\n')
	if !bytes.Equal(writer.Body.Bytes(), expected) {
		t.Fatalf("成功响应字节不兼容:\n got = %q\nwant = %q", writer.Body.Bytes(), expected)
	}
	if len(writer.outcomes) != 1 || writer.outcomes[0].code != shared.CodeOK {
		t.Fatalf("成功路径 outcome = %+v", writer.outcomes)
	}
}

// TestWriteProtocolErrorWritesCompleteEnvelope 固定 writeProtocolError 的字节格式：
// 先 marshal 再写字头，响应体完整且请求日志 ID 来自包装器。
func TestWriteProtocolErrorWritesCompleteEnvelope(t *testing.T) {
	writer := newRPCRecordingWriter()
	writeProtocolError(writer, http.StatusBadRequest, "invalid request body")

	if writer.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", writer.Code, http.StatusBadRequest)
	}
	if contentType := writer.Header().Get("Content-Type"); contentType != "application/problem+json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	fields := decodeRPCJSON(t, writer.Body.Bytes())
	assertRPCField(t, fields, "code", jsonString("HTTP_400"))
	assertRPCField(t, fields, "message", jsonString("invalid request body"))
	assertRPCField(t, fields, "data", "null")
	assertRPCField(t, fields, "request_id", jsonString(rpcTestRequestID))
	assertRPCField(t, fields, "trace_id", jsonString(rpcTestTraceID))
	expected, err := json.Marshal(rpcResponse{
		Code: "HTTP_400", Message: "invalid request body", Data: nil,
		RequestID: rpcTestRequestID, TraceID: rpcTestTraceID,
	})
	if err != nil {
		t.Fatalf("构造期望响应: %v", err)
	}
	expected = append(expected, '\n')
	if !bytes.Equal(writer.Body.Bytes(), expected) {
		t.Fatalf("协议错误响应字节不兼容:\n got = %q\nwant = %q", writer.Body.Bytes(), expected)
	}
}

// TestProtocolErrorBodyFallsBackWhenEnvelopeCannotBeEncoded 覆盖 writeProtocolError
// 自身的兜底：连错误信封都无法 marshal 时仍返回合法 JSON（含 data 键与消息 ID）。
func TestProtocolErrorBodyFallsBackWhenEnvelopeCannotBeEncoded(t *testing.T) {
	body := protocolErrorBody(http.StatusInternalServerError, rpcResponse{
		Code:      "HTTP_500",
		Message:   "handler payload",
		Data:      math.NaN(),
		RequestID: rpcTestRequestID,
		TraceID:   rpcTestTraceID,
	})
	fields := decodeRPCJSON(t, body)
	assertRPCField(t, fields, "code", jsonString("HTTP_500"))
	assertRPCField(t, fields, "message", jsonString("response encoding failed"))
	assertRPCField(t, fields, "data", "null")
	assertRPCField(t, fields, "request_id", jsonString(rpcTestRequestID))
	assertRPCField(t, fields, "trace_id", jsonString(rpcTestTraceID))
	if bytes.Contains(body, []byte("NaN")) {
		t.Fatalf("兜底响应体包含不可编码值: %q", body)
	}
}
