package panel

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

	"lattix/backend/internal/store"
)

const (
	releaseKindAgent = "agent"
	releaseKindXray  = "xray"
)

type releaseInspectionSettings struct {
	Agent inspectionSchedule `json:"agent"`
	Xray  inspectionSchedule `json:"xray"`
}

func defaultReleaseInspectionSettings() releaseInspectionSettings {
	daily := inspectionSchedule{Every: 1, Unit: "day", At: "03:00"}
	return releaseInspectionSettings{Agent: daily, Xray: daily}
}

type releaseCache struct {
	Versions  []string  `json:"versions"`
	FetchedAt time.Time `json:"fetched_at"`
}

type releaseVersionsDTO struct {
	Kind      string   `json:"kind"`
	Versions  []string `json:"versions"`
	FetchedAt string   `json:"fetched_at"`
	Stale     bool     `json:"stale"`
	Message   string   `json:"message,omitempty"`
}

type releaseCatalog struct {
	s       *Server
	client  *http.Client
	apiBase string

	mu     sync.Mutex
	cache  map[string]releaseCache
	errors map[string]string
	loaded map[string]bool
}

func newReleaseCatalog(s *Server) *releaseCatalog {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("LATX_GITHUB_API_BASE")), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	return &releaseCatalog{
		s: s, client: &http.Client{Timeout: 30 * time.Second}, apiBase: base,
		cache: make(map[string]releaseCache), errors: make(map[string]string),
		loaded: make(map[string]bool),
	}
}

func cacheSettingKey(kind string) string {
	if kind == releaseKindAgent {
		return store.SettingAgentReleaseCache
	}
	return store.SettingXrayReleaseCache
}

func (c *releaseCatalog) repository(kind string) (string, error) {
	switch kind {
	case releaseKindAgent:
		if c.s.cfg.GitHubRepo == "" {
			return "", errors.New("未配置 agent GitHub 仓库")
		}
		return c.s.cfg.GitHubRepo, nil
	case releaseKindXray:
		return "XTLS/Xray-core", nil
	default:
		return "", errors.New("kind 须为 agent 或 xray")
	}
}

func (c *releaseCatalog) loadLocked(ctx context.Context, kind string) {
	if c.loaded[kind] {
		return
	}
	c.loaded[kind] = true
	raw, err := c.s.st.GetSetting(ctx, cacheSettingKey(kind))
	if err != nil || raw == "" {
		return
	}
	var cached releaseCache
	if json.Unmarshal([]byte(raw), &cached) == nil && len(cached.Versions) > 0 {
		c.cache[kind] = cached
	}
}

func (c *releaseCatalog) get(ctx context.Context, kind string) (releaseVersionsDTO, error) {
	if _, err := c.repository(kind); err != nil {
		return releaseVersionsDTO{}, err
	}
	c.mu.Lock()
	c.loadLocked(ctx, kind)
	cached := c.cache[kind]
	c.mu.Unlock()
	if len(cached.Versions) == 0 {
		if err := c.refresh(ctx, kind); err != nil {
			return releaseVersionsDTO{}, err
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
	return releaseVersionsDTO{
		Kind: kind, Versions: versions, FetchedAt: cached.FetchedAt.Format(time.RFC3339),
		Stale: message != "", Message: message,
	}, nil
}

func (c *releaseCatalog) refresh(ctx context.Context, kind string) error {
	repo, err := c.repository(kind)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiBase+"/repos/"+repo+"/releases?per_page=100", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Lattix-panel")
	resp, err := c.client.Do(req)
	if err != nil {
		return c.rememberError(kind, fmt.Errorf("拉取 GitHub release 失败: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.rememberError(kind, fmt.Errorf("拉取 GitHub release 失败: HTTP %d", resp.StatusCode))
	}
	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return c.rememberError(kind, fmt.Errorf("解析 GitHub release 失败: %w", err))
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
	if err := c.s.st.SetSetting(ctx, cacheSettingKey(kind), string(raw)); err != nil {
		return c.rememberError(kind, err)
	}
	c.mu.Lock()
	c.cache[kind] = cached
	c.loaded[kind] = true
	delete(c.errors, kind)
	c.mu.Unlock()
	return nil
}

func (c *releaseCatalog) rememberError(kind string, err error) error {
	c.mu.Lock()
	c.errors[kind] = err.Error()
	c.mu.Unlock()
	return err
}

func (s *Server) releaseInspectionSettings(ctx context.Context) releaseInspectionSettings {
	defaults := defaultReleaseInspectionSettings()
	raw := s.getSetting(ctx, store.SettingReleaseInspection)
	if raw == "" {
		return defaults
	}
	var settings releaseInspectionSettings
	if json.Unmarshal([]byte(raw), &settings) != nil || settings.Agent.validate() != nil || settings.Xray.validate() != nil {
		return defaults
	}
	return settings
}

func (s *Server) inspectionLocation(ctx context.Context) *time.Location {
	name := s.getSetting(ctx, store.SettingTimezone)
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

func (s *Server) handleListReleaseVersions(w http.ResponseWriter, r *http.Request) {
	result, err := s.releases.get(r.Context(), strings.TrimSpace(r.URL.Query().Get("kind")))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
