package panel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"lattix/backend/internal/dispatch"
	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
	"lattix/backend/internal/sub"
)

// 手动刷新返回成功时，订阅地址必须已经交付当前分组的全部格式，而非仅入队。
func TestManualRefreshPublishesCurrentGroupsBeforeReturning(t *testing.T) {
	ctx := context.Background()
	st, chainA, chainB, _, userID, _ := groupsFixture(t)
	// VLESS 不支持 QuanX 节点输出；添加各客户端都支持的 SS 外部节点，验证全部格式。
	seedExternal := func(name string) int64 {
		t.Helper()
		id, err := st.CreateExternalSubscription(ctx, store.ExternalSubscription{Name: "source-" + name, URL: "https://example.com/" + name, UpdateIntervalHours: 24})
		if err != nil {
			t.Fatal(err)
		}
		config, err := json.Marshal(extsub.Node{Name: name, Type: "ss", Server: "proxy.example.com", Port: 8388, Extra: map[string]any{"method": "aes-128-gcm", "password": "password"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ReplaceExternalChains(ctx, id, []store.ExternalChain{{SubscriptionID: id, Name: name, Protocol: "ss", Server: "proxy.example.com", Port: 8388, Config: config, ConfigSHA256: name}}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldExternal, newExternal := seedExternal("outer-old"), seedExternal("outer-new")
	lg, err := st.CreateLinkGroup(ctx, "links", []int64{chainA}, []store.LinkGroupExternalSubscription{{SubscriptionID: oldExternal, Mode: store.ExtSubModeStack}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "users", []int64{userID}, []int64{lg}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	initial, err := srv.subscriptions.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateLinkGroup(ctx, lg, "links", []int64{chainB}, []store.LinkGroupExternalSubscription{{SubscriptionID: newExternal, Mode: store.ExtSubModeStack}}); err != nil {
		t.Fatal(err)
	}
	code, status := postGroup(t, srv, srv.handleRegenerateUserSubscription, "/api/user/regenerate-subscription", fmt.Sprintf(`{"user_id":%d}`, userID))
	if status != http.StatusOK || code != "OK" {
		t.Fatalf("refresh status=%d code=%s", status, code)
	}
	for _, format := range []string{"clash", "singbox", "quanx", "quanx-config", "links"} {
		file, err := st.PublishedSubscriptionFile(ctx, userID, format)
		if err != nil {
			t.Fatal(err)
		}
		if file.Revision <= initial.Revision {
			t.Fatalf("manual refresh returned success before publishing %s", format)
		}
		if bytes.Equal(file.Content, initial.Files[format]) {
			t.Fatalf("%s content did not change after changing the link group", format)
		}
		r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/tok-a?format="+format, nil)
		r.SetPathValue("token", "tok-a")
		w := httptest.NewRecorder()
		srv.subscriptions.ServeHTTP(w, r)
		if !bytes.Equal(w.Body.Bytes(), file.Content) {
			t.Fatalf("%s subscription URL did not deliver the newly published file", format)
		}
		content := w.Body.String()
		if format == "links" {
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				t.Fatal(err)
			}
			content = string(decoded)
		}
		body, err := url.QueryUnescape(content)
		if err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || !strings.Contains(body, "outer-new") || strings.Contains(body, "outer-old") || strings.Contains(body, "h-a") || format != "quanx" && !strings.Contains(body, "h-b") {
			t.Fatalf("%s subscription did not apply current group: status=%d\n%s", format, w.Code, body)
		}
		t.Logf("%s: content changed; old nodes removed, current group nodes present", format)
	}
}

func TestTemplateReassignmentRewritesDeliveredFormats(t *testing.T) {
	ctx := context.Background()
	st, chainID, _, _, userID, _ := groupsFixture(t)
	if _, _, err := st.SetUserChains(ctx, userID, []int64{chainID}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	for _, name := range []string{"Policy-A", "Policy-B"} {
		upsertTestTemplate(t, st, store.SubscriptionTemplate{ID: name, Name: name, Kind: "portable", Origin: "local", Content: strings.ReplaceAll(portableTemplateBody, "Proxy", name)})
	}
	var previousRevision int64
	previousContents := make(map[string][]byte)
	for _, name := range []string{"Policy-A", "Policy-B"} {
		status, code := assignRequest(t, srv, fmt.Sprintf(`{"user_ids":[%d],"template_id":%q,"forced":true,"region_groups_enabled":false}`, userID, name))
		if status != http.StatusOK || code != "OK" {
			t.Fatalf("assignment status=%d code=%s", status, code)
		}
		for _, format := range []string{"clash", "singbox", "quanx-config", "quanx", "links"} {
			r := httptest.NewRequest(http.MethodGet, "https://panel.example/sub/tok-a?format="+format, nil)
			r.SetPathValue("token", "tok-a")
			w := httptest.NewRecorder()
			srv.subscriptions.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", format, w.Code, w.Body.String())
			}
			file, err := st.PublishedSubscriptionFile(ctx, userID, format)
			if err != nil {
				t.Fatal(err)
			}
			if file.Revision <= previousRevision {
				t.Fatalf("%s kept old revision after assignment", format)
			}
			if format != "quanx" && format != "links" && (!strings.Contains(w.Body.String(), name) || name == "Policy-B" && strings.Contains(w.Body.String(), "Policy-A")) {
				t.Fatalf("%s did not apply assigned %s:\n%s", format, name, w.Body.String())
			}
			policyFormat := format != "quanx" && format != "links"
			if previous, ok := previousContents[format]; ok {
				changed := !bytes.Equal(previous, w.Body.Bytes())
				if changed != policyFormat {
					t.Fatalf("%s content changed=%t after policy reassignment, want %t", format, changed, policyFormat)
				}
				t.Logf("%s: reassignment content changed=%t as expected", format, changed)
			}
			previousContents[format] = append([]byte(nil), w.Body.Bytes()...)
			switch format {
			case "clash":
				var config struct {
					Rules []string `yaml:"rules"`
				}
				if err := yaml.Unmarshal(w.Body.Bytes(), &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Rules) != 1 || config.Rules[0] != "MATCH,"+name {
					t.Fatalf("clash rules=%v, want assigned final policy %s", config.Rules, name)
				}
			case "singbox":
				var config struct {
					Route struct {
						Final string `json:"final"`
					} `json:"route"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &config); err != nil {
					t.Fatal(err)
				}
				if config.Route.Final != name {
					t.Fatalf("singbox final=%q, want %q", config.Route.Final, name)
				}
			case "quanx-config":
				if !strings.Contains(w.Body.String(), "\nFINAL,"+name+"\n") {
					t.Fatalf("quanx final rule did not follow assigned %s", name)
				}
			}
		}
		file, err := st.PublishedSubscriptionFile(ctx, userID, "clash")
		if err != nil {
			t.Fatal(err)
		}
		previousRevision = file.Revision
	}
}

func TestManualRefreshReportsGenerationFailure(t *testing.T) {
	ctx := context.Background()
	st, _, _, _, userID, _ := groupsFixture(t)
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	initial, err := srv.subscriptions.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := st.UserSubscriptionProfile(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	profile.Mode, profile.PortableTemplateID = store.SubscriptionModeTemplate, "missing"
	if err := st.SaveUserSubscriptionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	code, _ := postGroup(t, srv, srv.handleRegenerateUserSubscription, "/api/user/regenerate-subscription", fmt.Sprintf(`{"user_id":%d}`, userID))
	if code == "OK" {
		t.Fatal("manual refresh reported success despite invalid template")
	}
	status, err := st.SubscriptionSnapshotStatus(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != store.SubscriptionGenerationError || status.Revision != initial.Revision {
		t.Fatalf("generation failure must be visible and preserve previous snapshot: %+v", status)
	}
}

func TestInvalidatedChainRegeneratesGroupedUserSnapshot(t *testing.T) {
	ctx := context.Background()
	st, chainID, _, _, userID, _ := groupsFixture(t)
	lg, err := st.CreateLinkGroup(ctx, "links", []int64{chainID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUserGroup(ctx, "users", []int64{userID}, []int64{lg}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
	srv.disp = dispatch.New(st, &chainEditRequester{}, dispatch.Options{}, dispatch.Events{OnChainPublished: srv.onChainPublished})
	startTestRegenerator(t, srv.subscriptions)
	initial, err := srv.subscriptions.PublishUser(ctx, userID, "https://panel.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(initial.Files["clash"]), "h-a") {
		t.Fatal("initial snapshot missing assigned chain")
	}
	revision, err := st.PublishedChainRevision(ctx, chainID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.disp.ChainFSM().InvalidateForServerDeletion(ctx, chainID, revision.Snapshot.ServiceServerID, "server deleted"); err != nil {
		t.Fatal(err)
	}
	file := awaitSubscriptionFileRevision(t, st, userID, "clash", initial.Revision)
	if strings.Contains(string(file.Content), "h-a") {
		t.Fatal("invalidated chain remains in snapshot")
	}
}

func TestTemplateAssignmentControlsRegionGroups(t *testing.T) {
	for _, kind := range []string{"suggested", "portable", "mihomo", "singbox"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			st, chainID, _, _, userID, _ := groupsFixture(t)
			if _, _, err := st.SetUserChains(ctx, userID, []int64{chainID}); err != nil {
				t.Fatal(err)
			}
			srv := &Server{st: st, subscriptions: sub.New(st, nil, nil)}
			startTestRegenerator(t, srv.subscriptions)
			target, format := `"suggested_categories":["private"]`, "clash"
			if kind != "suggested" {
				content := portableTemplateBody
				if kind == "mihomo" {
					content = "proxy-groups:\n  - name: Proxy\n    type: select\n    proxies: [__LATTIX_ALL__, __LATTIX_REGIONS__]\n"
				}
				if kind == "singbox" {
					content, format = `{"outbounds":[{"tag":"Proxy","type":"selector","outbounds":["__LATTIX_ALL__","__LATTIX_REGIONS__"]}]}`, "singbox"
				}
				upsertTestTemplate(t, st, store.SubscriptionTemplate{ID: "test-regions", Name: "regions", Kind: kind, Origin: "local", Content: content})
				target = `"template_id":"test-regions"`
			}
			var revision int64
			for _, enabled := range []bool{false, true} {
				status, code := assignRequest(t, srv, fmt.Sprintf(`{"user_ids":[%d],%s,"forced":true,"region_groups_enabled":%t}`, userID, target, enabled))
				if status != http.StatusOK {
					t.Fatalf("assign status=%d code=%s", status, code)
				}
				file := awaitSubscriptionFileRevision(t, st, userID, format, revision)
				revision = file.Revision
				var document struct {
					Groups []struct {
						Name string `yaml:"name"`
					} `yaml:"proxy-groups"`
					Outbounds []struct {
						Tag string `yaml:"tag"`
					} `yaml:"outbounds"`
				}
				if err := yaml.Unmarshal(file.Content, &document); err != nil {
					t.Fatal(err)
				}
				hasRegion := false
				for _, group := range document.Groups {
					hasRegion = hasRegion || group.Name == "🇯🇵 JP"
				}
				for _, group := range document.Outbounds {
					hasRegion = hasRegion || group.Tag == "🇯🇵 JP"
				}
				if hasRegion != enabled {
					t.Fatalf("region group present=%t, enabled=%t\n%s", hasRegion, enabled, file.Content)
				}
				if !strings.Contains(string(file.Content), "h-a") || !strings.Contains(string(file.Content), "Lattix 分组") {
					t.Fatal("disabling regions removed nodes or source group")
				}
			}
		})
	}
}
