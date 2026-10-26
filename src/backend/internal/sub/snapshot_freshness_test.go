package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"lattix/backend/internal/store"
)

func TestSubscriptionRequestRefreshesQueuedChainChanges(t *testing.T) {
	for _, change := range []string{"add", "edit", "delete"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			st, userID := setupChainFixture(t, true)
			srv := New(st, nil, nil)
			assignments, err := st.UserChainAssignments(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			chainID := assignments[0].ChainID
			if change == "add" {
				if _, _, err := st.SetUserChains(ctx, userID, nil); err != nil {
					t.Fatal(err)
				}
			}
			initial, err := srv.PublishUser(ctx, userID, "https://panel.example")
			if err != nil {
				t.Fatal(err)
			}
			want, unwanted := "JP直连Test", ""
			switch change {
			case "add":
				if _, _, err := st.SetUserChains(ctx, userID, []int64{chainID}); err != nil {
					t.Fatal(err)
				}
			case "edit":
				previous, err := st.PublishedChainRevision(ctx, chainID)
				if err != nil {
					t.Fatal(err)
				}
				snapshot := previous.Snapshot
				snapshot.Name = "更新后的链路"
				revision, err := st.CreateChainRevision(ctx, chainID, snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if err := st.PublishChainRevision(ctx, revision.ID, false); err != nil {
					t.Fatal(err)
				}
				want, unwanted = snapshot.Name, "JP直连Test"
			case "delete":
				if err := st.DeleteChain(ctx, chainID); err != nil {
					t.Fatal(err)
				}
				want, unwanted = "", "JP直连Test"
			}
			srv.EnqueueUsers([]int64{userID}, "https://panel.example")
			// 后台已取走批次但尚未发布的窗口也不能交付旧快照。
			srv.takeQueued()
			r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/bean-token?format=clash", nil)
			r.SetPathValue("token", "bean-token")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
			if err != nil {
				t.Fatal(err)
			}
			if file.Revision <= initial.Revision {
				t.Fatalf("served stale revision %d after %s", file.Revision, change)
			}
			if want != "" && !strings.Contains(w.Body.String(), want) {
				t.Fatalf("subscription missing %q", want)
			}
			if unwanted != "" && strings.Contains(w.Body.String(), unwanted) {
				t.Fatalf("subscription still contains %q", unwanted)
			}
		})
	}
}

func TestConcurrentSubscriptionRequestsPublishPendingVersionOnce(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	srv := New(st, nil, nil)
	initial, err := srv.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.SetUserChains(ctx, userID, nil); err != nil {
		t.Fatal(err)
	}
	srv.EnqueueUsers([]int64{userID}, "https://panel.example")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/bean-token?format=clash", nil)
			r.SetPathValue("token", "bean-token")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "JP直连Test") {
				t.Errorf("concurrent request served stale data: status=%d", w.Code)
			}
		})
	}
	wg.Wait()
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil {
		t.Fatal(err)
	}
	if file.Revision != initial.Revision+1 {
		t.Fatalf("revision=%d, want one publish after %d", file.Revision, initial.Revision)
	}
}

func TestQueuedPublicationKeepsLatestBaseURLAfterBatchTaken(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	srv := New(st, nil, nil)
	body := "name: Cached\ngroups:\n  - name: Proxy\n    type: select\n    options: [DIRECT]\nrules: []\nremote_rules:\n  - name: sample\n    url: https://raw.githubusercontent.com/example/rules/main/sample.list\n    behavior: classical\n    outbound: Proxy\nfinal: Proxy\n"
	template := store.SubscriptionTemplate{ID: "remote", Name: "remote", Kind: "portable", Origin: "local", Content: body, ContentSHA256: contentSHA(body)}
	rules := "DOMAIN-SUFFIX,example.com\n"
	if err := st.UpsertSubscriptionTemplateWithRules(ctx, template, []store.SubscriptionTemplateRule{{TemplateID: template.ID, TemplateSHA256: template.ContentSHA256, Name: "sample", SourceURL: "https://raw.githubusercontent.com/example/rules/main/sample.list", Content: []byte(rules), ContentSHA256: contentSHA(rules)}}, true); err != nil {
		t.Fatal(err)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Mode, profile.PortableTemplateID = store.SubscriptionModeTemplate, template.ID
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	srv.EnqueueUsers([]int64{userID}, "https://old.example")
	srv.takeQueued()
	srv.EnqueueUsers([]int64{userID}, "https://new.example")
	srv.takeQueued()
	if err := srv.publishPendingUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Content), "https://new.example/sub/") || strings.Contains(string(file.Content), "https://old.example/sub/") {
		t.Fatalf("published obsolete rule URL:\n%s", file.Content)
	}
	// 重启后尚无 remembered URL；链路事件的空地址任务须使用本次订阅请求地址。
	afterRestart := New(st, nil, nil)
	afterRestart.EnqueueUsers([]int64{userID}, "")
	r := httptest.NewRequest(http.MethodGet, "https://request.example/sub/bean-token?format=clash", nil)
	r.SetPathValue("token", "bean-token")
	w := httptest.NewRecorder()
	afterRestart.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "https://request.example/sub/") {
		t.Fatalf("empty-URL event did not use subscription request address: status=%d\n%s", w.Code, w.Body.String())
	}
}

func TestSubscriptionRequestDoesNotServeStaleSnapshotWhenRefreshFails(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	srv := New(st, nil, nil)
	if _, err := srv.PublishUser(ctx, userID, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Mode, profile.PortableTemplateID = store.SubscriptionModeTemplate, "missing-template"
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	srv.EnqueueUsers([]int64{userID}, "https://panel.example")
	r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/bean-token?format=clash", nil)
	r.SetPathValue("token", "bean-token")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503 instead of stale snapshot", w.Code)
	}
}
