package testcatalog

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

//go:embed education_targets.json
var educationJSON []byte

//go:embed speed_targets.json
var speedJSON []byte

//go:embed connectivity_targets.json
var connectivityJSON []byte

// ConnectivityGroups is the fixed set of region groups in display order.
var ConnectivityGroups = []string{"cn", "jp", "us", "gl"}

type Source struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path,omitempty"`
	Note       string `json:"note"`
}

type EducationTarget struct {
	Province string `json:"province"`
	Label    string `json:"label"`
	Host     string `json:"host"`
}

type SpeedTarget struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Host       string `json:"host"`
	SNI        string `json:"sni"`
	Path          string `json:"path,omitempty"`
	UploadPath    string `json:"upload_path,omitempty"`
	Family        string `json:"family,omitempty"`
	OoklaServerID string `json:"ookla_server_id,omitempty"`
}

type ConnectivityTarget struct {
	Group string `json:"group"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
	Host  string `json:"host"`
	Path  string `json:"path"`
}

type Catalog struct {
	Education    []EducationTarget
	Speed        []SpeedTarget
	Connectivity []ConnectivityTarget
	Hashes       map[string]string
}

func Load() (Catalog, error) {
	type educationDocument struct {
		Version int               `json:"version"`
		Source  Source            `json:"source"`
		Targets []EducationTarget `json:"targets"`
	}
	type speedDocument struct {
		Version int           `json:"version"`
		Source  Source        `json:"source"`
		Targets []SpeedTarget `json:"targets"`
	}
	type connectivityDocument struct {
		Version int                  `json:"version"`
		Source  Source               `json:"source"`
		Targets []ConnectivityTarget `json:"targets"`
	}
	var education educationDocument
	var speed speedDocument
	var connectivity connectivityDocument
	if err := json.Unmarshal(educationJSON, &education); err != nil {
		return Catalog{}, fmt.Errorf("decode education targets: %w", err)
	}
	if err := json.Unmarshal(speedJSON, &speed); err != nil {
		return Catalog{}, fmt.Errorf("decode speed targets: %w", err)
	}
	if err := json.Unmarshal(connectivityJSON, &connectivity); err != nil {
		return Catalog{}, fmt.Errorf("decode connectivity targets: %w", err)
	}
	if education.Version != 1 || speed.Version != 1 || connectivity.Version != 1 {
		return Catalog{}, errors.New("unsupported static test catalog version")
	}
	if len(education.Targets) != 31 || len(speed.Targets) != 14 {
		return Catalog{}, fmt.Errorf("unexpected static catalog counts: education=%d speed=%d",
			len(education.Targets), len(speed.Targets))
	}
	// 76 = ip.net.coffee 48 站点 ∪ TcpQuality 国际站点（同品牌去重，见目录 source.note）。
	if len(connectivity.Targets) != 76 {
		return Catalog{}, fmt.Errorf("unexpected connectivity catalog count: %d", len(connectivity.Targets))
	}
	validGroup := func(group string) bool {
		for _, candidate := range ConnectivityGroups {
			if group == candidate {
				return true
			}
		}
		return false
	}
	seen := make(map[string]struct{})
	validateHost := func(host string) error {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || strings.Contains(host, "://") || strings.Contains(host, "/") ||
			strings.Contains(host, "nodeseek") || strings.Contains(host, "ibsgss") {
			return fmt.Errorf("forbidden static target %q", host)
		}
		if _, exists := seen[host]; exists {
			return nil
		}
		seen[host] = struct{}{}
		return nil
	}
	for _, target := range education.Targets {
		if target.Province == "" || target.Label == "" || !strings.HasSuffix(target.Host, ".edu.cn") {
			return Catalog{}, fmt.Errorf("invalid education target %+v", target)
		}
		if err := validateHost(target.Host); err != nil {
			return Catalog{}, err
		}
	}
	for _, target := range speed.Targets {
		if target.ID == "" || target.Label == "" {
			return Catalog{}, fmt.Errorf("invalid speed target %+v", target)
		}
		if target.OoklaServerID != "" {
			// Ookla targets are resolved by the official speedtest CLI at
			// runtime; they carry no host of their own.
			if _, err := strconv.Atoi(target.OoklaServerID); err != nil {
				return Catalog{}, fmt.Errorf("invalid ookla server id %q", target.OoklaServerID)
			}
			continue
		}
		if target.SNI != target.Host {
			return Catalog{}, fmt.Errorf("invalid speed target %+v", target)
		}
		if err := validateHost(target.Host); err != nil {
			return Catalog{}, err
		}
	}
	connectivitySlugs := make(map[string]struct{}, len(connectivity.Targets))
	for _, target := range connectivity.Targets {
		if !validGroup(target.Group) || target.Slug == "" || target.Label == "" || !strings.HasPrefix(target.Path, "/") {
			return Catalog{}, fmt.Errorf("invalid connectivity target %+v", target)
		}
		if err := validateHost(target.Host); err != nil {
			return Catalog{}, err
		}
		if _, exists := connectivitySlugs[target.Slug]; exists {
			return Catalog{}, fmt.Errorf("duplicate connectivity slug %q", target.Slug)
		}
		connectivitySlugs[target.Slug] = struct{}{}
	}
	return Catalog{
		Education: education.Targets, Speed: speed.Targets, Connectivity: connectivity.Targets,
		Hashes: map[string]string{
			"education": hash(educationJSON), "speed": hash(speedJSON),
			"connectivity": hash(connectivityJSON),
		},
	}, nil
}

func hash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
