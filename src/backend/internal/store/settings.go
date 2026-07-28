package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"lattix/shared"
)

// 面板设置键（§10 设置页）。DB 中有值即优先于对应启动参数，重启后由 main 读取生效。
const (
	SettingTimezone        = "timezone"          // IANA 时区名（面板时间显示）；空 = 浏览器本地
	SettingPublicURL       = "public_url"        // 面板对外地址（含端口），生成安装命令/订阅链接
	SettingTLSMode         = "tls_mode"          // off|cert|acme|path；空 = 未配置（跟随启动参数）
	SettingTLSCertPEM      = "tls_cert_pem"      // 自带证书 PEM（tls_mode=cert）
	SettingTLSKeyPEM       = "tls_key_pem"       // 私钥 PEM（tls_mode=cert）
	SettingTLSDomain       = "tls_domain"        // 域名路径模式域名（tls_mode=path）：证书位于 <tls-dir>/<域名>/
	SettingACMEDomain      = "acme_domain"       // ACME 自动证书域名（tls_mode=acme）
	SettingACMEEmail       = "acme_email"        // ACME 账号邮箱（可选）
	SettingAdminPassBcrypt = "admin_pass_bcrypt" // 管理员密码 bcrypt 哈希；空 = 使用 -admin-pass
	// 事件告警（§19）：三项全空 = 告警关闭；webhook 单独可发，telegram 需 token+chat_id 同时具备。
	SettingAlertWebhookURL       = "alert_webhook_url"        // Webhook 接收端（POST JSON）
	SettingAlertTelegramBotToken = "alert_telegram_bot_token" // Telegram Bot token（不回显，仅置位标记）
	SettingAlertTelegramChatID   = "alert_telegram_chat_id"   // Telegram 会话 ID
	SettingOperationLogLimit     = "operation_log_limit"      // 操作日志最多保留条数，默认 1000
	SettingRequestLogMaxMB       = "request_log_max_mb"       // 请求日志 JSONL 总容量 MiB，默认 10
	SettingPanelInstanceID       = "panel_instance_id"
	SettingAgentSettings         = "agent_settings"
	SettingReleaseInspection     = "release_inspection"
	SettingAgentReleaseCache     = "agent_release_cache"
	SettingXrayReleaseCache      = "xray_release_cache"
)

// GetSetting 读取一个设置；未设置返回空字符串（不视为错误）。
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting 写入一个设置（upsert）。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// DeleteSetting 删除一个设置（恢复跟随启动参数）。
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}

// PanelInstanceID returns the stable identity of this logical panel. Database
// backup/restore retains it; a fresh database creates a new identity.
func (s *Store) PanelInstanceID(ctx context.Context) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var value string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, SettingPanelInstanceID).Scan(&value)
	if err == nil {
		return value, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("get panel instance id: %w", err)
	}
	value, err = shared.NewPanelInstanceID()
	if err != nil {
		return "", fmt.Errorf("create panel instance id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, SettingPanelInstanceID, value); err != nil {
		return "", fmt.Errorf("save panel instance id: %w", err)
	}
	return value, tx.Commit()
}

// AgentSettings returns the global desired Agent settings, creating defaults
// on first use.
func (s *Store) AgentSettings(ctx context.Context) (shared.AgentSettings, error) {
	raw, err := s.GetSetting(ctx, SettingAgentSettings)
	if err != nil {
		return shared.AgentSettings{}, err
	}
	if raw == "" {
		defaults := shared.DefaultAgentSettings()
		if err := s.saveAgentSettings(ctx, defaults); err != nil {
			return shared.AgentSettings{}, err
		}
		return defaults, nil
	}
	var settings shared.AgentSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return shared.AgentSettings{}, fmt.Errorf("decode agent settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return shared.AgentSettings{}, fmt.Errorf("validate stored agent settings: %w", err)
	}
	return settings, nil
}

// UpdateAgentSettings replaces the desired object and increments its revision
// in the same database transaction.
func (s *Store) UpdateAgentSettings(ctx context.Context, desired shared.AgentSettings) (shared.AgentSettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return shared.AgentSettings{}, err
	}
	defer tx.Rollback()
	current := shared.DefaultAgentSettings()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, SettingAgentSettings).Scan(&raw)
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &current); err != nil {
			return shared.AgentSettings{}, fmt.Errorf("decode agent settings: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return shared.AgentSettings{}, fmt.Errorf("get agent settings: %w", err)
	}
	desired.Revision = current.Revision + 1
	if err := desired.Validate(); err != nil {
		return shared.AgentSettings{}, err
	}
	encoded, err := json.Marshal(desired)
	if err != nil {
		return shared.AgentSettings{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		SettingAgentSettings, string(encoded)); err != nil {
		return shared.AgentSettings{}, fmt.Errorf("save agent settings: %w", err)
	}
	return desired, tx.Commit()
}

func (s *Store) saveAgentSettings(ctx context.Context, settings shared.AgentSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, SettingAgentSettings, string(raw))
}
