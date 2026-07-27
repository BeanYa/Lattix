package requester

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalRequestersUseUpstreamHTTPSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":"ok"}`))
		case "/webhook":
			w.WriteHeader(http.StatusNoContent)
		case "/file":
			_, _ = w.Write([]byte("artifact"))
		default:
			http.Error(w, "upstream failed", http.StatusBadGateway)
		}
	}))
	defer server.Close()

	doer := server.Client()
	var payload struct {
		Value string `json:"value"`
	}
	if err := (ExternalJSONRequester{Doer: doer}).GetJSON(
		context.Background(), server.URL+"/json", &payload,
	); err != nil || payload.Value != "ok" {
		t.Fatalf("GetJSON = %#v, %v", payload, err)
	}
	if err := (ExternalWebhookRequester{Doer: doer}).PostJSON(
		context.Background(), server.URL+"/webhook", map[string]string{"event": "test"},
	); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}

	path := filepath.Join(t.TempDir(), "artifact")
	if err := (ExternalFileRequester{Doer: doer}).Download(
		context.Background(), server.URL+"/file", path, nil,
	); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "artifact" {
		t.Fatalf("downloaded = %q, %v", body, err)
	}
	if err := (ExternalWebhookRequester{Doer: doer}).PostJSON(
		context.Background(), server.URL+"/failure", struct{}{},
	); err == nil {
		t.Fatal("upstream non-2xx status was accepted")
	}
}
