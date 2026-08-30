package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lattix/backend/internal/panel/releases"
	"lattix/backend/internal/store"
)

func (s *Server) releaseInspectionSettings(ctx context.Context) releases.InspectionSettings {
	defaults := releases.DefaultInspectionSettings()
	raw := s.getSetting(ctx, store.SettingReleaseInspection)
	if raw == "" {
		return defaults
	}
	var settings releases.InspectionSettings
	if json.Unmarshal([]byte(raw), &settings) != nil || settings.Agent.Validate() != nil || settings.Xray.Validate() != nil {
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
	result, err := s.releases.Get(r.Context(), strings.TrimSpace(r.URL.Query().Get("kind")))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
