// Package releases 维护 agent/xray 的 GitHub release 版本目录：从上游拉取
// release 列表并缓存到设置表（拉取失败保留旧缓存并标记 stale），配套巡检调度配置。
package releases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lattix/backend/internal/panel/scheduler"
	"lattix/backend/internal/store"
	external "lattix/shared/requester"
)

const (
	KindAgent = "agent"
	KindXray  = "xray"
)

type InspectionSettings struct {
	Agent scheduler.InspectionSchedule `json:"agent"`
	Xray  scheduler.InspectionSchedule `json:"xray"`
}

func DefaultInspectionSettings() InspectionSettings {
	daily := scheduler.InspectionSchedule{Every: 1, Unit: "day", At: "03:00"}
	return InspectionSettings{Agent: daily, Xray: daily}
}

type releaseCache struct {
	Versions  []string  `json:"versions"`
	FetchedAt time.Time `json:"fetched_at"`
}

type Versions struct {
	Kind      string   `json:"kind"`
	Versions  []string `json:"versions"`
	FetchedAt string   `json:"fetched_at"`
	Stale     bool     `json:"stale"`
	Message   string   `json:"message,omitempty"`
}

type Catalog struct {
	st         *store.Store
	githubRepo string
	api        external.ExternalJSONRequester
	apiBase    string

	mu     sync.Mutex
	cache  map[string]releaseCache
	errors map[string]string
	loaded map[string]bool
}

func New(st *store.Store, githubRepo string) *Catalog {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LATX_GITHUB_API_BASE")), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	return &Catalog{
		st: st, githubRepo: githubRepo, api: external.ExternalJSONRequester{Doer: &http.Client{Timeout: 30 * time.Second}}, apiBase: base,
		cache: make(map[string]releaseCache), errors: make(map[string]string),
		loaded: make(map[string]bool),
	}
}

func cacheSettingKey(kind string) string {
	if kind == KindAgent {
		return store.SettingAgentReleaseCache
	}
	return store.SettingXrayReleaseCache
}

func (c *Catalog) repository(kind string) (string, error) {
	switch kind {
	case KindAgent:
		if c.githubRepo == "" {
			return "", errors.New("未配置 agent GitHub 仓库")
		}
		return c.githubRepo, nil
	case KindXray:
		return "XTLS/Xray-core", nil
	default:
		return "", errors.New("kind 须为 agent 或 xray")
	}
}

func (c *Catalog) loadLocked(ctx context.Context, kind string) {
	if c.loaded[kind] {
		return
	}
	c.loaded[kind] = true
	raw, err := c.st.GetSetting(ctx, cacheSettingKey(kind))
	if err != nil || raw == "" {
		return
	}
	var cached releaseCache
	if json.Unmarshal([]byte(raw), &cached) == nil && len(cached.Versions) > 0 {
		c.cache[kind] = cached
	}
}

func (c *Catalog) Get(ctx context.Context, kind string) (Versions, error) {
	if _, err := c.repository(kind); err != nil {
		return Versions{}, err
	}
	c.mu.Lock()
	c.loadLocked(ctx, kind)
	cached := c.cache[kind]
	c.mu.Unlock()
	if len(cached.Versions) == 0 {
		if err := c.Refresh(ctx, kind); err != nil {
			return Versions{}, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cached = c.cache[kind]
	message := c.errors[kind]
	versions := make([]string, 1, len(cached.Versions)+1)
	versions[0] = "latest"
	for _, version := range cached.Versions {
		if !strings.EqualFold(version, "latest") {
			versions = append(versions, version)
		}
	}
	return Versions{
		Kind: kind, Versions: versions, FetchedAt: cached.FetchedAt.Format(time.RFC3339),
		Stale: message != "", Message: message,
	}, nil
}

func (c *Catalog) Refresh(ctx context.Context, kind string) error {
	repo, err := c.repository(kind)
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("Accept", "application/vnd.github+json")
	header.Set("User-Agent", "Lattix-panel")
	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	err = c.api.GetWithOptions(ctx, c.apiBase+"/repos/"+repo+"/releases?per_page=100", &releases,
		external.JSONRequestOptions{Header: header})
	if err != nil {
		return c.rememberError(kind, fmt.Errorf("拉取 GitHub release 失败: %w", err))
	}
	seen := make(map[string]bool)
	versions := make([]string, 0, len(releases))
	for _, release := range releases {
		tag := strings.TrimSpace(release.TagName)
		if release.Draft || tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		versions = append(versions, tag)
	}
	if len(versions) == 0 {
		return c.rememberError(kind, errors.New("GitHub 未返回可用 release 版本"))
	}
	cached := releaseCache{Versions: versions, FetchedAt: time.Now().UTC()}
	raw, _ := json.Marshal(cached)
	if err := c.st.SetSetting(ctx, cacheSettingKey(kind), string(raw)); err != nil {
		return c.rememberError(kind, err)
	}
	c.mu.Lock()
	c.cache[kind] = cached
	c.loaded[kind] = true
	delete(c.errors, kind)
	c.mu.Unlock()
	return nil
}

func (c *Catalog) rememberError(kind string, err error) error {
	c.mu.Lock()
	c.errors[kind] = err.Error()
	c.mu.Unlock()
	return err
}
