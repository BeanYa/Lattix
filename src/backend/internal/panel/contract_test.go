package panel

import (
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/logging"
)

func TestCertInfoDTOEncodesEmptyDNSNamesAsArray(t *testing.T) {
	dto := toCertInfoDTO(&x509.Certificate{})
	if dto.DNSNames == nil {
		t.Fatal("DNSNames is nil; JSON contract requires an empty array")
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal certificate DTO: %v", err)
	}
	if !regexp.MustCompile(`"dns_names":\[\]`).Match(raw) {
		t.Fatalf("certificate JSON = %s, want empty dns_names array", raw)
	}
}

func TestWriteProtocolErrorAlwaysIncludesMessageIDs(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteProtocolError(recorder, http.StatusNotFound, "missing")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	var response rpcResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "HTTP_404" || !sharedMessageIDPattern.MatchString(response.RequestID) {
		t.Fatalf("response = %+v", response)
	}
	if response.TraceID != response.RequestID {
		t.Fatalf("trace_id = %q, want request_id %q", response.TraceID, response.RequestID)
	}
}

var sharedMessageIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestOpenAPIRoutesMatchRegisteredRPCs(t *testing.T) {
	raw := readOpenAPIDocument(t)
	var document struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}

	documented := make(map[string]struct{})
	for path, item := range document.Paths {
		for method := range item {
			method = strings.ToUpper(method)
			if method == http.MethodGet || method == http.MethodPost {
				documented[method+" "+path] = struct{}{}
			}
		}
	}
	server := &Server{routePolicies: make(map[string]logging.LogPolicy)}
	server.RegisterRoutes(http.NewServeMux())

	missingFromSpec := setDifference(server.routePolicies, documented)
	missingFromServer := setDifference(documented, server.routePolicies)
	if len(missingFromSpec) != 0 || len(missingFromServer) != 0 {
		t.Fatalf("route contract drift: undocumented=%v unregistered=%v", missingFromSpec, missingFromServer)
	}
}

func TestOpenAPIDocumentsRateLimitResponses(t *testing.T) {
	raw := readOpenAPIDocument(t)
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Ref string `yaml:"$ref"`
			} `yaml:"responses"`
		} `yaml:"paths"`
		Components struct {
			Responses map[string]struct {
				Headers map[string]any `yaml:"headers"`
			} `yaml:"responses"`
			Schemas struct {
				ProtocolError struct {
					Properties struct {
						Code struct {
							Enum []string `yaml:"enum"`
						} `yaml:"code"`
					} `yaml:"properties"`
				} `yaml:"ProtocolError"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse OpenAPI document: %v", err)
	}

	const rateLimitRef = "#/components/responses/RateLimitErrorResponse"
	for _, path := range []string{"/api/auth/login", "/api/setting/change-password"} {
		operation, ok := document.Paths[path]["post"]
		if !ok {
			t.Fatalf("OpenAPI operation POST %s is missing", path)
		}
		response, ok := operation.Responses["429"]
		if !ok || response.Ref != rateLimitRef {
			t.Fatalf("POST %s 429 response = %+v, want %s", path, response, rateLimitRef)
		}
	}

	rateLimitResponse, ok := document.Components.Responses["RateLimitErrorResponse"]
	if !ok {
		t.Fatal("RateLimitErrorResponse is missing")
	}
	if _, ok := rateLimitResponse.Headers["Retry-After"]; !ok {
		t.Fatal("RateLimitErrorResponse must document Retry-After")
	}
	if !containsString(document.Components.Schemas.ProtocolError.Properties.Code.Enum, "HTTP_429") {
		t.Fatal("ProtocolError.code must include HTTP_429")
	}
}

func readOpenAPIDocument(t *testing.T) []byte {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract test source")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "../../../../docs/openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI document: %v", err)
	}
	return raw
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func setDifference[T, U any](left map[string]T, right map[string]U) []string {
	result := make([]string, 0)
	for key := range left {
		if _, ok := right[key]; !ok {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
