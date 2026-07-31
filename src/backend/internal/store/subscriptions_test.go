package store

import (
	"context"
	"testing"
)

func TestSubscriptionSnapshotPublicationIsAtomic(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	userID, err := st.InsertUser(ctx, "snapshot", "00000000-0000-0000-0000-000000000001", "snapshot-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	formats := map[string]SubscriptionFile{}
	for _, format := range []string{"clash", "singbox", "quanx", "quanx-config", "links"} {
		formats[format] = SubscriptionFile{Format: format, ContentType: "text/plain", Content: []byte("first-" + format)}
	}
	first, err := st.PublishSubscriptionSnapshot(ctx, userID, "first", "source-1", formats, []SubscriptionRuleFile{{
		Name: "remote-1", Format: "mihomo", SourceSHA256: "rule-version", ContentType: "text/yaml", Content: []byte("payload: []\n"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("revision = %d, want 1", first.Revision)
	}
	for format := range formats {
		file, err := st.PublishedSubscriptionFile(ctx, userID, format)
		if err != nil || file.Revision != 1 {
			t.Fatalf("published %s = revision %d, err %v", format, file.Revision, err)
		}
	}
	rule, err := st.SubscriptionRuleFile(ctx, userID, "rule-version", "mihomo", "remote-1")
	if err != nil || rule.Revision != 1 {
		t.Fatalf("rule revision = %d, err %v", rule.Revision, err)
	}

	_, err = st.PublishSubscriptionSnapshot(ctx, userID, "broken", "source-2", formats, []SubscriptionRuleFile{
		{Name: "duplicate", Format: "mihomo", SourceSHA256: "v2", ContentType: "text/yaml", Content: []byte("one")},
		{Name: "duplicate", Format: "mihomo", SourceSHA256: "v2", ContentType: "text/yaml", Content: []byte("two")},
	})
	if err == nil {
		t.Fatal("publication with duplicate rule unexpectedly succeeded")
	}
	file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
	if err != nil {
		t.Fatal(err)
	}
	if file.Revision != 1 || string(file.Content) != "first-clash" {
		t.Fatalf("published pointer changed after failed transaction: revision=%d content=%q", file.Revision, file.Content)
	}
}

func TestTemplateAndRuleCacheReplaceAtomically(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	template := SubscriptionTemplate{ID: "cache-test", Name: "cache", Kind: "portable", Origin: "local", Content: "v1", ContentSHA256: "sha-1"}
	rules := []SubscriptionTemplateRule{{TemplateID: template.ID, TemplateSHA256: "sha-1", Name: "one", SourceURL: "https://raw.githubusercontent.com/a/b/main/one.list", Content: []byte("DOMAIN,a.example"), ContentSHA256: "rule-1"}}
	if err := st.UpsertSubscriptionTemplateWithRules(ctx, template, rules, true); err != nil {
		t.Fatal(err)
	}
	template.Content, template.ContentSHA256 = "v2", "sha-2"
	duplicate := []SubscriptionTemplateRule{
		{TemplateID: template.ID, TemplateSHA256: "sha-2", Name: "same", SourceURL: "https://raw.githubusercontent.com/a/b/main/a.list", Content: []byte("a"), ContentSHA256: "a"},
		{TemplateID: template.ID, TemplateSHA256: "sha-2", Name: "same", SourceURL: "https://raw.githubusercontent.com/a/b/main/b.list", Content: []byte("b"), ContentSHA256: "b"},
	}
	if err := st.UpsertSubscriptionTemplateWithRules(ctx, template, duplicate, true); err == nil {
		t.Fatal("duplicate rule cache unexpectedly succeeded")
	}
	got, err := st.SubscriptionTemplateByID(ctx, template.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentSHA256 != "sha-1" || got.Content != "v1" {
		t.Fatalf("template cache changed after failed transaction: %+v", got)
	}
	gotRules, err := st.SubscriptionTemplateRules(ctx, template.ID, "sha-1")
	if err != nil || len(gotRules) != 1 || gotRules[0].ContentSHA256 != "rule-1" {
		t.Fatalf("rule cache changed after failed transaction: %+v, err %v", gotRules, err)
	}
}
