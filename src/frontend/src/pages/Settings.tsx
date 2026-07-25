import { useEffect, useRef, useState, type FormEvent } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { useTimezone } from '@/lib/timezone'
import type { PanelSettings } from '@/lib/types'

// 常用 IANA 时区（可直接输入其他名称，后端用 time.LoadLocation 校验）。
const COMMON_TIMEZONES = [
  'Asia/Shanghai',
  'Asia/Hong_Kong',
  'Asia/Taipei',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Dubai',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Moscow',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Australia/Sydney',
  'Pacific/Auckland',
  'UTC',
]

// UI 侧 TLS 模式：'flag' 对应后端的空值（跟随启动参数），避免 Base UI 空串值被当作"未选择"。
type TLSModeChoice = 'flag' | 'off' | 'cert' | 'acme' | 'path'

const TLS_MODES: { value: TLSModeChoice; label: string }[] = [
  { value: 'flag', label: '跟随启动参数' },
  { value: 'off', label: '关闭 HTTPS（HTTP 监听）' },
  { value: 'cert', label: '自带证书（PEM）' },
  { value: 'acme', label: 'ACME 自动证书（Let\'s Encrypt）' },
  { value: 'path', label: '域名路径（外部 ACME 证书目录）' },
]

const RUNNING_MODE_LABEL: Record<string, string> = {
  off: 'HTTP',
  cert: 'HTTPS（自带证书）',
  acme: 'HTTPS（ACME）',
  path: 'HTTPS（域名路径）',
}

const textareaClass =
  'w-full min-w-0 rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-xs transition-colors outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30'

export default function Settings() {
  const { refresh: refreshTimezone } = useTimezone()
  const [settings, setSettings] = useState<PanelSettings | null>(null)
  const [loadError, setLoadError] = useState('')

  // 基本设置
  const [publicURL, setPublicURL] = useState('')
  const [timezone, setTimezone] = useState('')
  // TLS
  const [tlsMode, setTlsMode] = useState<TLSModeChoice>('flag')
  const [certPEM, setCertPEM] = useState('')
  const [keyPEM, setKeyPEM] = useState('')
  const [tlsDomain, setTlsDomain] = useState('')
  const [acmeDomain, setAcmeDomain] = useState('')
  const [acmeEmail, setAcmeEmail] = useState('')
  // 密码
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  const [saving, setSaving] = useState(false)
  const [savingPassword, setSavingPassword] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [passwordMessage, setPasswordMessage] = useState('')
  const [passwordError, setPasswordError] = useState('')

  const certFileRef = useRef<HTMLInputElement>(null)
  const keyFileRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    api
      .settings()
      .then((s) => {
        setSettings(s)
        setPublicURL(s.public_url)
        setTimezone(s.timezone)
        setTlsMode(s.tls_mode === '' ? 'flag' : s.tls_mode)
        setTlsDomain(s.tls_domain)
        setAcmeDomain(s.acme_domain)
        setAcmeEmail(s.acme_email)
      })
      .catch((err) => setLoadError(errorMessage(err)))
  }, [])

  const readFileInto = (file: File | undefined, setter: (v: string) => void) => {
    if (!file) {
      return
    }
    const reader = new FileReader()
    reader.onload = () => setter(String(reader.result ?? ''))
    reader.readAsText(file)
  }

  const onSave = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setError('')
    setMessage('')
    try {
      const s = await api.updateSettings({
        timezone: timezone.trim(),
        public_url: publicURL.trim(),
        tls_mode: tlsMode === 'flag' ? '' : tlsMode,
        // 留空 = 保持已保存值不变（后端语义）
        ...(certPEM.trim() ? { tls_cert_pem: certPEM } : {}),
        ...(keyPEM.trim() ? { tls_key_pem: keyPEM } : {}),
        tls_domain: tlsDomain.trim(),
        acme_domain: acmeDomain.trim(),
        acme_email: acmeEmail.trim(),
      })
      setSettings(s)
      setCertPEM('')
      setKeyPEM('')
      refreshTimezone()
      setMessage(
        s.restart_required
          ? '已保存。TLS/证书设置将在面板进程重启后生效。'
          : '已保存并生效。',
      )
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const onChangePassword = async (e: FormEvent) => {
    e.preventDefault()
    setPasswordError('')
    setPasswordMessage('')
    if (newPassword !== confirmPassword) {
      setPasswordError('两次输入的新密码不一致')
      return
    }
    setSavingPassword(true)
    try {
      await api.changePassword(currentPassword, newPassword)
      setPasswordMessage('密码已修改，所有会话已失效，请重新登录。')
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (err) {
      setPasswordError(errorMessage(err))
    } finally {
      setSavingPassword(false)
    }
  }

  const onRestart = async () => {
    if (!window.confirm('确定重启面板进程？重启期间面板短暂不可用（数秒）。')) {
      return
    }
    setRestarting(true)
    try {
      await api.restartPanel()
    } catch {
      // 进程退出导致连接中断属预期
    }
    // 轮询等待面板恢复，恢复后整页刷新（设置/运行态可能已变化）
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 1500))
      try {
        await api.me()
        window.location.reload()
        return
      } catch {
        // 尚未恢复，继续等
      }
    }
    setRestarting(false)
    setError('等待重启完成超时。若切换了 HTTP/HTTPS 或端口，请改用新地址访问面板。')
  }

  if (loadError) {
    return <p className="text-sm text-destructive">{loadError}</p>
  }
  if (!settings) {
    return <p className="text-sm text-muted-foreground">加载中…</p>
  }

  return (
    <div className="max-w-2xl space-y-4">
      <h1 className="text-xl font-semibold">设置</h1>

      <form onSubmit={onSave} className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle>基本设置</CardTitle>
            <CardDescription>对外地址与时区保存后立即生效。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="publicURL">面板对外地址（含协议与端口）</Label>
              <Input
                id="publicURL"
                value={publicURL}
                onChange={(e) => setPublicURL(e.target.value)}
                placeholder="https://panel.example.com:8443，留空按请求推断"
              />
              <p className="text-xs text-muted-foreground">
                用于生成 agent 安装命令与订阅链接；反代部署时填反代后的地址（https 即安全入口）。
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="timezone">显示时区</Label>
              <Input
                id="timezone"
                list="timezone-list"
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                placeholder="留空使用浏览器本地时区"
              />
              <datalist id="timezone-list">
                {COMMON_TIMEZONES.map((tz) => (
                  <option key={tz} value={tz} />
                ))}
              </datalist>
              <p className="text-xs text-muted-foreground">
                IANA 时区名（如 Asia/Shanghai），全局生效：所有浏览器看到的面板时间一致。
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>面板证书（TLS）</CardTitle>
            <CardDescription>
              当前运行：
              <Badge variant="outline" className="mx-1">
                {RUNNING_MODE_LABEL[settings.running_tls_mode] ?? settings.running_tls_mode}
              </Badge>
              ；TLS 设置在<strong>重启面板进程后生效</strong>。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {settings.restart_required && (
              <div className="flex items-center justify-between gap-2 rounded-lg border border-yellow-200 bg-yellow-50 px-3 py-2 text-sm text-yellow-700">
                <span>已保存的 TLS 设置与当前运行不一致，重启面板进程后生效。</span>
                <Button type="button" variant="outline" size="sm" disabled={restarting} onClick={onRestart}>
                  {restarting ? '重启中…' : '立即重启'}
                </Button>
              </div>
            )}
            <div className="space-y-2">
              <Label>TLS 模式</Label>
              <Select value={tlsMode} onValueChange={(v) => v && setTlsMode(v as TLSModeChoice)} items={TLS_MODES}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TLS_MODES.map((m) => (
                    <SelectItem key={m.value} value={m.value}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {tlsMode === 'cert' && (
              <>
                {settings.tls_cert && (
                  <p className="text-xs text-muted-foreground">
                    已保存证书：CN={settings.tls_cert.common_name || '-'}
                    {settings.tls_cert.dns_names.length > 0 &&
                      `，SAN=${settings.tls_cert.dns_names.join(', ')}`}
                    ，到期 {formatDateTime(settings.tls_cert.not_after)}
                    {settings.tls_cert.expired && <span className="text-destructive">（已过期）</span>}
                    {settings.tls_key_set && '；私钥已保存'}
                  </p>
                )}
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <Label htmlFor="certPEM">证书 PEM（留空保持不变）</Label>
                    <Button type="button" variant="outline" size="sm" onClick={() => certFileRef.current?.click()}>
                      上传文件
                    </Button>
                    <input
                      ref={certFileRef}
                      type="file"
                      accept=".pem,.crt,.cer,.txt"
                      className="hidden"
                      onChange={(e) => readFileInto(e.target.files?.[0], setCertPEM)}
                    />
                  </div>
                  <textarea
                    id="certPEM"
                    className={textareaClass}
                    rows={6}
                    value={certPEM}
                    onChange={(e) => setCertPEM(e.target.value)}
                    placeholder="-----BEGIN CERTIFICATE-----"
                  />
                </div>
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <Label htmlFor="keyPEM">私钥 PEM（留空保持不变）</Label>
                    <Button type="button" variant="outline" size="sm" onClick={() => keyFileRef.current?.click()}>
                      上传文件
                    </Button>
                    <input
                      ref={keyFileRef}
                      type="file"
                      accept=".pem,.key,.txt"
                      className="hidden"
                      onChange={(e) => readFileInto(e.target.files?.[0], setKeyPEM)}
                    />
                  </div>
                  <textarea
                    id="keyPEM"
                    className={textareaClass}
                    rows={6}
                    value={keyPEM}
                    onChange={(e) => setKeyPEM(e.target.value)}
                    placeholder="-----BEGIN PRIVATE KEY-----"
                  />
                </div>
              </>
            )}

            {tlsMode === 'path' && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="tlsDomain">证书域名</Label>
                  <Input
                    id="tlsDomain"
                    value={tlsDomain}
                    onChange={(e) => setTlsDomain(e.target.value)}
                    placeholder="panel.example.com"
                  />
                  <p className="text-xs text-muted-foreground">
                    面板从证书根目录 <code className="rounded bg-muted px-1">{settings.tls_dir}</code> 读取
                    <code className="rounded bg-muted px-1">{'<域名>/fullchain.pem'}</code> 与
                    <code className="rounded bg-muted px-1">{'<域名>/privkey.pem'}</code>
                    （如 <code className="rounded bg-muted px-1">{settings.tls_dir}/panel.example.com/fullchain.pem</code>）。
                    外部 ACME（安装脚本）申请/续期后写入该目录即可，续期替换文件后下一次
                    TLS 握手自动加载新证书，无需重启。保存时会校验证书已存在且配对有效。
                  </p>
                </div>
                {settings.tls_cert && (
                  <p className="text-xs text-muted-foreground">
                    目录内当前证书：CN={settings.tls_cert.common_name || '-'}
                    {settings.tls_cert.dns_names.length > 0 &&
                      `，SAN=${settings.tls_cert.dns_names.join(', ')}`}
                    ，到期 {formatDateTime(settings.tls_cert.not_after)}
                    {settings.tls_cert.expired && <span className="text-destructive">（已过期）</span>}
                  </p>
                )}
              </>
            )}

            {tlsMode === 'acme' && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="acmeDomain">ACME 域名</Label>
                  <Input
                    id="acmeDomain"
                    value={acmeDomain}
                    onChange={(e) => setAcmeDomain(e.target.value)}
                    placeholder="panel.example.com"
                  />
                  <p className="text-xs text-muted-foreground">
                    Let&apos;s Encrypt TLS-ALPN-01 签发，需 443 端口公网可达。
                  </p>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="acmeEmail">ACME 邮箱（可选）</Label>
                  <Input
                    id="acmeEmail"
                    value={acmeEmail}
                    onChange={(e) => setAcmeEmail(e.target.value)}
                    placeholder="过期通知邮箱，可留空"
                  />
                </div>
              </>
            )}
          </CardContent>
        </Card>

        {error && <p className="text-sm text-destructive">{error}</p>}
        {message && <p className="text-sm text-green-700">{message}</p>}
        <Button type="submit" disabled={saving}>
          {saving ? '保存中…' : '保存设置'}
        </Button>
      </form>

      <Card>
        <CardHeader>
          <CardTitle>修改密码</CardTitle>
          <CardDescription>
            账号 {settings.admin_user}
            {settings.password_override ? '（密码已被设置页覆盖）' : '（当前使用启动参数密码）'}
            ；修改后所有会话失效，需重新登录。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onChangePassword} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="currentPassword">当前密码</Label>
              <Input
                id="currentPassword"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                autoComplete="current-password"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="newPassword">新密码（至少 8 位）</Label>
              <Input
                id="newPassword"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="confirmPassword">确认新密码</Label>
              <Input
                id="confirmPassword"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            {passwordError && <p className="text-sm text-destructive">{passwordError}</p>}
            {passwordMessage && <p className="text-sm text-green-700">{passwordMessage}</p>}
            <Button type="submit" disabled={savingPassword || !currentPassword || !newPassword}>
              {savingPassword ? '修改中…' : '修改密码'}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>面板维护</CardTitle>
          <CardDescription>
            重启面板进程：TLS 等重启生效项、以及后续面板版本更新都经此生效。
            systemd 托管时退出后由 systemd 自动拉起；否则面板自派生新进程接管（同参数、同端口）。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button variant="outline" disabled={restarting} onClick={onRestart}>
            {restarting ? '重启中，请稍候…' : '重启面板'}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
