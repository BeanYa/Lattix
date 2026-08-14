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

//go:embed international_targets.json
var internationalJSON []byte

//go:embed education_targets.json
var educationJSON []byte

//go:embed speed_targets.json
var speedJSON []byte

type Source struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path,omitempty"`
	Note       string `json:"note"`
}

type InternationalTarget struct {
	Label string `json:"label"`
	Host  string `json:"host"`
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

type Catalog struct {
	International []InternationalTarget
	Education     []EducationTarget
	Speed         []SpeedTarget
	Hashes        map[string]string
}

func Load() (Catalog, error) {
	type internationalDocument struct {
		Version int                   `json:"version"`
		Source  Source                `json:"source"`
		Targets []InternationalTarget `json:"targets"`
	}
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
	var international internationalDocument
	var education educationDocument
	var speed speedDocument
	if err := json.Unmarshal(internationalJSON, &international); err != nil {
		return Catalog{}, fmt.Errorf("decode international targets: %w", err)
	}
	if err := json.Unmarshal(educationJSON, &education); err != nil {
		return Catalog{}, fmt.Errorf("decode education targets: %w", err)
	}
	if err := json.Unmarshal(speedJSON, &speed); err != nil {
		return Catalog{}, fmt.Errorf("decode speed targets: %w", err)
	}
	if international.Version != 1 || education.Version != 1 || speed.Version != 1 {
		return Catalog{}, errors.New("unsupported static test catalog version")
	}
	if len(international.Targets) != 44 || len(education.Targets) != 31 || len(speed.Targets) != 14 {
		return Catalog{}, fmt.Errorf("unexpected static catalog counts: international=%d education=%d speed=%d",
			len(international.Targets), len(education.Targets), len(speed.Targets))
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
	for _, target := range international.Targets {
		if target.Label == "" {
			return Catalog{}, errors.New("international target label is empty")
		}
		if err := validateHost(target.Host); err != nil {
			return Catalog{}, err
		}
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
	return Catalog{
		International: international.Targets, Education: education.Targets, Speed: speed.Targets,
		Hashes: map[string]string{
			"international": hash(internationalJSON), "education": hash(educationJSON), "speed": hash(speedJSON),
		},
	}, nil
}

func hash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
