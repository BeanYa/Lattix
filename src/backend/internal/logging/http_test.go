package logging

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeParametersRedactsAndTruncates(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet,
		"https://example.test/api/servers/7?token=secret&q="+strings.Repeat("a", 600), nil)
	request.Pattern = "GET /api/servers/{id}"
	request.SetPathValue("id", "7")

	params := safeParameters(request)
	if params["query.token"] != "[REDACTED]" {
		t.Fatalf("token = %q", params["query.token"])
	}
	if params["path.id"] != "7" {
		t.Fatalf("path id = %q", params["path.id"])
	}
	if !strings.HasSuffix(params["query.q"], "[TRUNCATED]") {
		t.Fatalf("long query was not truncated: %q", params["query.q"])
	}
}

func TestSafePathHashesSubscriptionToken(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://example.test/sub/super-secret/links", nil)
	request.Pattern = "GET /sub/{token}/links"
	request.SetPathValue("token", "super-secret")
	path := safePath(request)
	if strings.Contains(path, "super-secret") || !strings.Contains(path, "[token:") {
		t.Fatalf("unsafe subscription path %q", path)
	}
}

func TestRequestMiddlewareRecordsMetadataAndExcludesLogReader(t *testing.T) {
	log, err := OpenRequestLog(filepath.Join(t.TempDir(), "requests"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/test/{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	})
	mux.HandleFunc("GET /api/log/list-requests", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestMiddleware(log, func(*http.Request) string { return "admin" }, func(r *http.Request) LogPolicy {
		if r.URL.Path == "/api/log/list-requests" {
			return LogNone
		}
		return LogFull
	}, mux)

	request := httptest.NewRequest(http.MethodGet, "/api/test/7?token=secret", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.1")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/log/list-requests", nil))

	items, _, err := log.Tail(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("logged %d requests, want 1", len(items))
	}
	entry := items[0]
	if entry.HTTPStatus != http.StatusNotFound || entry.Severity != SeverityWarning {
		t.Fatalf("status/severity = %d/%s", entry.HTTPStatus, entry.Severity)
	}
	if entry.Route != "/api/test/{id}" || entry.Attributes["path.id"] != "7" {
		t.Fatalf("route/attributes = %q/%v", entry.Route, entry.Attributes)
	}
	if entry.Attributes["query.token"] != "[REDACTED]" {
		t.Fatalf("token = %q", entry.Attributes["query.token"])
	}
	if entry.IP != "198.51.100.8" {
		t.Fatalf("spoofed forwarded IP was trusted: %q", entry.IP)
	}
}
