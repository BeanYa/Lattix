package releases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lattix/backend/internal/store"
)

func TestReleaseCatalogCachesVersionsWithLatestFirst(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/BeanYa/Lattix/releases" {
			t.Fatalf("unexpected release path: %s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "Lattix-panel" {
			t.Fatalf("release request missing github headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"tag_name":"latest","draft":false},
			{"tag_name":"v2.0.0","draft":false},
			{"tag_name":"v1.0.0","draft":false},
			{"tag_name":"v3.0.0-rc1","draft":true}
		]`))
	}))

	st, err := store.Open(t.TempDir() + "/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	catalog := New(st, "BeanYa/Lattix")
	catalog.apiBase = upstream.URL

	got, err := catalog.Get(context.Background(), KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"latest", "v2.0.0", "v1.0.0"}
	if len(got.Versions) != len(want) {
		t.Fatalf("versions = %#v, want %#v", got.Versions, want)
	}
	for i := range want {
		if got.Versions[i] != want[i] {
			t.Fatalf("versions = %#v, want %#v", got.Versions, want)
		}
	}

	upstream.Close()
	reloaded := New(st, "BeanYa/Lattix")
	reloaded.apiBase = upstream.URL
	got, err = reloaded.Get(context.Background(), KindAgent)
	if err != nil {
		t.Fatalf("read persisted cache: %v", err)
	}
	if got.Versions[0] != "latest" || got.Stale {
		t.Fatalf("persisted cache = %#v", got)
	}
	if err := reloaded.Refresh(context.Background(), KindAgent); err == nil {
		t.Fatal("refresh unexpectedly succeeded after upstream closed")
	}
	got, err = reloaded.Get(context.Background(), KindAgent)
	if err != nil {
		t.Fatalf("stale cache should remain usable: %v", err)
	}
	if !got.Stale || got.Message == "" {
		t.Fatalf("failed refresh did not mark cache stale: %#v", got)
	}
}

func TestDefaultXrayInspectionIsDaily(t *testing.T) {
	schedule := DefaultInspectionSettings().Xray
	if schedule.Every != 1 || schedule.Unit != "day" || schedule.At == "" {
		t.Fatalf("unexpected xray schedule: %#v", schedule)
	}
}
