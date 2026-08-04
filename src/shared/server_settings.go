package shared

import (
	"errors"
	"fmt"
	"regexp"
)

const (
	ServerSettingsSchemaVersion = 1
)

var xrayVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// ServerSettings 是面板默认（defaultsetting）与服务器覆盖（customsetting）共用的
// 服务器设置模型（字段级覆盖，可扩展）。XrayVersion 为 nil = 未设置：
// default 中未设置 = 不自动对齐；custom 中未设置 = 继承 default。
type ServerSettings struct {
	XrayVersion *string `json:"xray_version,omitempty"`
}

// ServerSettingsDocument 是面板下发生效文档（agent 落盘，Task 6 使用）。
// Revision 是面板计算的 effective revision，agent 原样回执。
type ServerSettingsDocument struct {
	SchemaVersion int            `json:"schema_version"`
	Revision      int64          `json:"revision"`
	Server        ServerSettings `json:"server"`
}

// ServerSettingsSyncPayload 是 agent 上报已应用 revision 的载荷（照抄 AgentSettingsSyncPayload）。
type ServerSettingsSyncPayload struct {
	PanelInstanceID string `json:"panel_instance_id"`
	AppliedRevision int64  `json:"applied_revision"`
	LastApplyError  string `json:"last_apply_error,omitempty"`
}

// ServerSettingsSyncResult 是面板对 sync 请求的响应；Changed 时携带完整文档。
type ServerSettingsSyncResult struct {
	Changed  bool                     `json:"changed"`
	Settings *ServerSettingsDocument `json:"settings,omitempty"`
}

// ServerSettingsChangedPayload 是面板变更通知事件的载荷。
type ServerSettingsChangedPayload struct {
	Revision int64 `json:"revision"`
}

// ValidateXrayVersion 校验 xray 版本取值：空 | latest | vX.Y.Z。
func ValidateXrayVersion(version string) error {
	if version == "" || version == "latest" || xrayVersionPattern.MatchString(version) {
		return nil
	}
	return fmt.Errorf("xray 版本须为空、latest 或 vX.Y.Z: %s", version)
}

func (s ServerSettings) Validate() error {
	if s.XrayVersion != nil {
		return ValidateXrayVersion(*s.XrayVersion)
	}
	return nil
}

func (d ServerSettingsDocument) Validate() error {
	if d.SchemaVersion != ServerSettingsSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", d.SchemaVersion)
	}
	if d.Revision < 1 {
		return errors.New("server settings revision must be at least 1")
	}
	if d.Server.XrayVersion == nil {
		return errors.New("server.xray_version is required")
	}
	return d.Server.Validate()
}

// DefaultServerSettings 返回面板默认值：latest（保持现状行为，不自动对齐）。
func DefaultServerSettings() ServerSettings {
	version := "latest"
	return ServerSettings{XrayVersion: &version}
}
