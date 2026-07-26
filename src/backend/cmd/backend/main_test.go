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

func TestSPAHandlerServesAssetAndFallsBackToIndex(t *testing.T) {
	content := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<main>Lattix</main>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('ok')")},
		"assets/empty.css": &fstest.MapFile{Data: nil},
	}
	handler := spaHandler(fs.FS(content))

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/assets/app.js", want: "console.log('ok')"},
		{path: "/settings", want: "<main>Lattix</main>"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != tc.want {
			t.Fatalf("%s: status=%d body=%q", tc.path, rec.Code, rec.Body.String())
		}
	}
}
