package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
