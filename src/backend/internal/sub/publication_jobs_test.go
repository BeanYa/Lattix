package sub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/store"
)

func TestSubscriptionPublicationJobsShareFIFO(t *testing.T) {
	st, userID := setupChainFixture(t, true)
	ctx := context.Background()
	if _, err := st.InsertUser(ctx, "other", "other-uuid", "other-token", nil); err != nil {
		t.Fatal(err)
	}
	upsertTemplateConfigTemplate(t, st, "invalid-quanx", "Invalid QuanX", "quanx")
	s := New(st, nil, nil)
	initial, err := s.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	visible := string(initial.Files["clash"])
	if !strings.Contains(visible, "JP直连Test") {
		t.Fatal("test chain missing from initial snapshot")
	}
	r := func(path, token, body string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "https://panel.example/api/sub/"+token+path, strings.NewReader(body))
		req.SetPathValue("token", token)
		return req
	}
	post := func(req *http.Request, handler http.HandlerFunc) store.SubscriptionPublicationJob {
		t.Helper()
		rec := httptest.NewRecorder()
		handler(rec, req)
		var job store.SubscriptionPublicationJob
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s = %d, want 202: %s", req.URL.Path, rec.Code, rec.Body)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job.ID == "" || job.Status != store.PublicationQueued {
			t.Fatalf("invalid accepted job %+v: %v", job, err)
		}
		return job
	}

	visibility := post(r("/visibility", "bean-token", `{"hidden_ids":["chain:1"]}`), s.HandleSubUpdateVisibility)
	if visibility.QueuedBehind || visibility.Kind != store.PublicationVisibility {
		t.Fatalf("first job = %+v", visibility)
	}
	for _, ids := range []string{`null`, `["chain:+1"]`, `["chain:9999"]`} {
		rec := httptest.NewRecorder()
		s.HandleSubUpdateVisibility(rec, r("/visibility", "bean-token", `{"hidden_ids":`+ids+`}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid hidden_ids %s = %d: %s", ids, rec.Code, rec.Body)
		}
	}
	template := r("/template-config", "bean-token", `{"mode":"suggested","preset":"minimal","categories":["ai"],"quanx_template_id":"invalid-quanx"}`)
	template.Header.Set("Prefer", "respond-async")
	failing := post(template, s.HandleSubUpdateTemplateConfig)
	if !failing.QueuedBehind || failing.Kind != store.PublicationTemplate {
		t.Fatalf("template job = %+v", failing)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := s.SubscriptionVisibility(r("/visibility", "bean-token", ""), user)
	if err != nil || !slices.Equal(editor.HiddenIDs, []string{"chain:1"}) {
		t.Fatalf("editor queued visibility = %+v, %v", editor, err)
	}
	admin, err := s.UpdateSubscriptionVisibility(r("/visibility", "admin", ""), user, []string{})
	if err != nil || !admin.QueuedBehind || admin.Kind != store.PublicationVisibility {
		t.Fatalf("admin visibility job = %+v, %v", admin, err)
	}
	otherJobs := httptest.NewRecorder()
	s.HandleSubPublicationJobs(otherJobs, r("/publication-jobs", "other-token", ""))
	if otherJobs.Code != http.StatusOK || strings.TrimSpace(otherJobs.Body.String()) != `{"jobs":[]}` {
		t.Fatalf("another token observed jobs: %d %s", otherJobs.Code, otherJobs.Body)
	}

	assertSnapshot := func(revision int64, hasChain bool) {
		t.Helper()
		file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
		if err != nil || file.Revision != revision || strings.Contains(string(file.Content), "JP直连Test") != hasChain {
			t.Fatalf("revision %d, want %d, has chain %v, want %v (err %v)", file.Revision, revision,
				strings.Contains(string(file.Content), "JP直连Test"), hasChain, err)
		}
	}
	process := func(job store.SubscriptionPublicationJob, status string) {
		t.Helper()
		lock := s.publishLock(userID)
		lock.Lock()
		err := s.processNextSubscriptionJobLocked(ctx, userID)
		lock.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		result, err := st.SubscriptionPublicationJob(ctx, userID, job.ID)
		if err != nil || result.Status != status {
			t.Fatalf("job %+v, want %s, err %v", result, status, err)
		}
	}
	assertSnapshot(initial.Revision, true)
	s.EnqueueUsers([]int64{userID}, "https://panel.example")
	process(visibility, store.PublicationCompleted)
	assertSnapshot(initial.Revision+1, false)
	if err := s.publishPendingUser(ctx, userID); err != nil {
		t.Fatal(err)
	}
	assertSnapshot(initial.Revision+1, false)
	process(failing, store.PublicationFailed)
	assertSnapshot(initial.Revision+1, false)
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil || profile.UserConfigured || profile.GenerationStatus != store.SubscriptionGenerationReady {
		t.Fatalf("failed template affected published profile %+v: %v", profile, err)
	}
	process(admin, store.PublicationCompleted)
	assertSnapshot(initial.Revision+2, true)

	reset := r("/template-config/reset", "bean-token", "")
	reset.Header.Set("Prefer", "respond-async")
	process(post(reset, s.HandleSubResetTemplateConfig), store.PublicationCompleted)
	assertSnapshot(initial.Revision+3, true)
}

func TestSubscriptionPublicationJobsResumeAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.sqlite")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := st.InsertUser(ctx, "test", "resume-uuid", "resume-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.EnqueueSubscriptionTemplate(ctx, store.SubscriptionProfile{
		UserID: userID, Mode: store.SubscriptionModeSuggested, Preset: "minimal", CategoriesJSON: `["ai"]`,
	}, nil, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.EnqueueSubscriptionTemplateReset(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimSubscriptionPublicationJob(ctx, userID)
	if err != nil || claimed.ID != first.ID || claimed.Status != store.PublicationRunning {
		t.Fatalf("claimed job = %+v, %v", claimed, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, nil, nil)
	worker, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.StartRegenerator(worker)
	defer func() {
		cancel()
		if err := s.WaitRegenerator(ctx); err != nil {
			t.Error(err)
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-worker.Done():
			t.Fatal("restarted worker did not finish the queued jobs")
		case <-tick.C:
		}
		jobs, err := st.SubscriptionPublicationJobs(ctx, userID)
		if err != nil || len(jobs) != 2 || jobs[0].ID != second.ID || jobs[1].ID != first.ID {
			t.Fatalf("recovered jobs = %+v, %v", jobs, err)
		}
		if jobs[0].Status == store.PublicationCompleted && jobs[1].Status == store.PublicationCompleted {
			break
		}
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil || profile.UserConfigured {
		t.Fatalf("reset did not run after template save: %+v, %v", profile, err)
	}
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil || file.Revision != 2 {
		t.Fatalf("snapshot revision after recovery = %d, %v", file.Revision, err)
	}
}

func TestSubscriptionPublicationRetainsUnavailableHiddenItems(t *testing.T) {
	ctx := context.Background()
	st, userID := setupChainFixture(t, true)
	s := New(st, nil, nil)
	available := map[string]bool{"chain:1": true}
	if _, err := st.EnqueueSubscriptionVisibility(ctx, userID, []string{"chain:1"}, available, "https://panel.example"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteChain(ctx, 1); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://panel.example/api/sub/bean-token/visibility", nil)
	if _, err := s.UpdateSubscriptionVisibility(r, user, []string{}); err != nil {
		t.Fatal(err)
	}
	pending, err := st.UserRequestedHiddenSubscriptionItems(ctx, userID)
	if err != nil || !slices.Equal(pending, []string{"chain:1"}) {
		t.Fatalf("pending hidden items = %v, %v", pending, err)
	}
	applied, err := st.UserHiddenSubscriptionItems(ctx, userID)
	if err != nil || len(applied) != 0 {
		t.Fatalf("unpublished choice leaked into applied settings: %v, %v", applied, err)
	}
	lock := s.publishLock(userID)
	lock.Lock()
	defer lock.Unlock()
	for range 2 {
		if err := s.processNextSubscriptionJobLocked(ctx, userID); err != nil {
			t.Fatal(err)
		}
	}
	applied, err = st.UserHiddenSubscriptionItems(ctx, userID)
	if err != nil || !applied["chain:1"] {
		t.Fatalf("unavailable hidden item was dropped: %v, %v", applied, err)
	}
}
