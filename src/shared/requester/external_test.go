package requester

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

type failingExternalDoer struct{}

func (failingExternalDoer) Do(req *http.Request) (*http.Response, error) {
	return nil, errors.New("dial failed for " + req.URL.String())
}

func TestExternalRequesterRedactsSecretsFromErrors(t *testing.T) {
	rawURL := "https://alice:password@example.com/bot-secret/sendMessage?token=query-secret#fragment-secret"
	err := (ExternalWebhookRequester{Doer: failingExternalDoer{}}).PostJSON(
		context.Background(), rawURL, struct{}{},
	)
	if err == nil {
		t.Fatal("PostJSON unexpectedly succeeded")
	}
	message := err.Error()
	for _, secret := range []string{
		"alice", "password", "bot-secret", "query-secret", "fragment-secret",
	} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked %q: %s", secret, message)
		}
	}
	if !strings.Contains(message, "https://example.com") {
		t.Fatalf("error omitted safe destination context: %s", message)
	}
}

func TestGetWithOptionsSetsUserAgentAndReturnsHeader(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=3; expire=4")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	r := ExternalFileRequester{Doer: &http.Client{Timeout: 5 * time.Second}}
	result, err := r.GetWithOptions(context.Background(), srv.URL, 1024, FileRequestOptions{UserAgent: "clash-meta/2.4.0"})
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != "clash-meta/2.4.0" {
		t.Fatalf("user-agent = %q", gotUA)
	}
	if result.Body != "hello" {
		t.Fatalf("body = %q", result.Body)
	}
	if got := result.Header.Get("subscription-userinfo"); got != "upload=1; download=2; total=3; expire=4" {
		t.Fatalf("userinfo header = %q", got)
	}
}

// headerCapturingDoer captures the User-Agent as seen by the requester before
// Go's http.Transport injects its default "Go-http-client/1.1", so tests can
// assert what the requester itself sets.
type headerCapturingDoer struct {
	userAgent *string
}

func (d headerCapturingDoer) Do(req *http.Request) (*http.Response, error) {
	*d.userAgent = req.Header.Get("User-Agent")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}, nil
}

func TestGetTextWithOptionsOmitsUserAgentByDefault(t *testing.T) {
	var gotUA string
	r := ExternalFileRequester{Doer: headerCapturingDoer{userAgent: &gotUA}}
	if _, err := r.GetTextWithOptions(context.Background(), "https://example.com/file", 1024, FileRequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if gotUA != "" {
		t.Fatalf("user-agent = %q, want empty", gotUA)
	}
}
