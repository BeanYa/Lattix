package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestDefaultTLSDirUsesCurrentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := defaultTLSDir(), filepath.Join(home, "cert"); got != want {
		t.Fatalf("defaultTLSDir() = %q, want %q", got, want)
	}
}

func TestHTTPServerHasResourceLimits(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	srv := newHTTPServer(":8080", handler)
	if srv.ReadHeaderTimeout != httpReadHeaderTimeout || srv.ReadTimeout != httpReadTimeout {
		t.Fatalf("read timeouts = (%s, %s)", srv.ReadHeaderTimeout, srv.ReadTimeout)
	}
	if srv.WriteTimeout != httpWriteTimeout || srv.IdleTimeout != httpIdleTimeout {
		t.Fatalf("write/idle timeouts = (%s, %s)", srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, httpMaxHeaderBytes)
	}
}

func TestSecurityHeadersMiddlewareSetsHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	rec := httptest.NewRecorder()
	securityHeadersMiddleware(next).ServeHTTP(rec, req)

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "strict-origin-when-cross-origin",
		"Content-Security-Policy": "frame-ancestors 'none'",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestSPAHandlerServesAssetAndFallsBackToIndex(t *testing.T) {
	content := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<main>Lattix</main>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('ok')")},
		"assets/empty.css": &fstest.MapFile{Data: nil},
	}
	handler := spaHandler(fs.FS(content))

	for _, tc := range []struct {
		path      string
		want      string
		wantCache string
	}{
		{path: "/assets/app.js", want: "console.log('ok')", wantCache: "public, max-age=31536000, immutable"},
		{path: "/settings", want: "<main>Lattix</main>", wantCache: "no-cache"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
			t.Fatalf("%s: status=%d body=%q", tc.path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("%s: Cache-Control=%q, want %q", tc.path, got, tc.wantCache)
		}
	}
}
