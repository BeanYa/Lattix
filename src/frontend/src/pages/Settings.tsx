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
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { useTimezone } from '@/lib/timezone'
import type { AlertTestResult, PanelSettings, PanelVersionInfo } from '@/lib/types'

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

function formatBytes(bytes: number) {
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export default function Settings() {
  const { refresh: refreshTimezone } = useTimezone()
  const accessProtocol = window.location.protocol === 'https:' ? 'HTTPS' : 'HTTP'
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
  // 告警
  const [alertWebhook, setAlertWebhook] = useState('')
  const [alertBotToken, setAlertBotToken] = useState('')
  const [alertChatID, setAlertChatID] = useState('')
  // 日志
  const [operationLogLimit, setOperationLogLimit] = useState(1000)
  const [requestLogMaxMB, setRequestLogMaxMB] = useState(10)
  // Agent（面板统一下发）
  const [reconnectMode, setReconnectMode] = useState<'infinite' | 'limited'>('infinite')
  const [reconnectMaxRetries, setReconnectMaxRetries] = useState(10)
  const [telemetrySeconds, setTelemetrySeconds] = useState(60)
  const [driftSeconds, setDriftSeconds] = useState(15)
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
  const [testingAlerts, setTestingAlerts] = useState(false)
  const [alertTestResult, setAlertTestResult] = useState<AlertTestResult | null>(null)
  const [alertTestError, setAlertTestError] = useState('')
  // 面板更新
  const [versionInfo, setVersionInfo] = useState<PanelVersionInfo | null>(null)
  const [checkingUpdate, setCheckingUpdate] = useState(false)
  const [startingUpdate, setStartingUpdate] = useState(false)
  const [updateError, setUpdateError] = useState('')

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
        setAlertWebhook(s.alert_webhook_url)
        setAlertChatID(s.alert_telegram_chat_id)
        setOperationLogLimit(s.operation_log_limit)
        setRequestLogMaxMB(s.request_log_max_mb)
        setReconnectMode(s.agent.reconnect.mode)
        setReconnectMaxRetries(s.agent.reconnect.max_retries)
        setTelemetrySeconds(s.agent.telemetry.interval_seconds)
        setDriftSeconds(s.agent.drift_detection.interval_seconds)
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
        alert_webhook_url: alertWebhook.trim(),
        alert_telegram_chat_id: alertChatID.trim(),
        operation_log_limit: operationLogLimit,
        request_log_max_mb: requestLogMaxMB,
        agent: {
          revision: settings?.agent.revision ?? 1,
          reconnect: {
            mode: reconnectMode,
            max_retries: reconnectMaxRetries,
          },
          telemetry: { interval_seconds: telemetrySeconds },
          drift_detection: { interval_seconds: driftSeconds },
        },
        // bot token 留空 = 保持已保存值（后端语义，与 tls key 一致）
        ...(alertBotToken.trim() ? { alert_telegram_bot_token: alertBotToken.trim() } : {}),
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

  const onTestAlerts = async () => {
    setTestingAlerts(true)
    setAlertTestResult(null)
    setAlertTestError('')
    try {
      setAlertTestResult(await api.testAlerts())
    } catch (err) {
      setAlertTestError(errorMessage(err))
    } finally {
      setTestingAlerts(false)
    }
  }

  // 检查面板更新（以 GitHub release 最新版本为标准）。
  const onCheckUpdate = async () => {
    setCheckingUpdate(true)
    setUpdateError('')
    try {
      setVersionInfo(await api.panelVersion())
    } catch (err) {
      setVersionInfo(null)
      setUpdateError(errorMessage(err))
    } finally {
      setCheckingUpdate(false)
    }
  }

  // 启动面板自更新：后续进度由全局 UpdateOverlay 接管（锁定操作 + 进度可视化）。
  const onStartUpdate = async () => {
    const target = versionInfo?.latest ?? '最新版本'
    if (
      !window.confirm(
        `确定将面板更新到 ${target}？\n更新期间面板操作将被锁定，完成后自动重启（短暂不可用）。`,
      )
    ) {
      return
    }
    setStartingUpdate(true)
    setUpdateError('')
    try {
      await api.startPanelUpdate()
    } catch (err) {
      setUpdateError(errorMessage(err))
    } finally {
      setStartingUpdate(false)
    }
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
            <CardTitle>Agent</CardTitle>
            <CardDescription>
              所有 Agent 使用同一份设置。保存后 revision 自动递增，在线 Agent 会立即拉取，离线 Agent 在重连后同步。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label>重连策略</Label>
              <Select
                value={reconnectMode}
                onValueChange={(value) => value && setReconnectMode(value as 'infinite' | 'limited')}
                items={[
                  { value: 'infinite', label: '无限重试（默认）' },
                  { value: 'limited', label: '限制快速重试次数' },
                ]}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="infinite">无限重试（默认）</SelectItem>
                    <SelectItem value="limited">限制快速重试次数</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                两种策略均使用指数退避。限制次数耗尽或认证失败后，Agent 仍会每 5 分钟低频探测，不会永久停止。
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="reconnectMaxRetries">最大快速重试次数</Label>
              <Input
                id="reconnectMaxRetries"
                type="number"
                min={1}
                max={100}
                disabled={reconnectMode === 'infinite'}
                value={reconnectMaxRetries}
                onChange={(event) => setReconnectMaxRetries(Number(event.target.value))}
              />
              <p className="text-xs text-muted-foreground">范围 1-100；无限重试模式会保存但忽略该值。</p>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label htmlFor="telemetrySeconds">遥测间隔（秒）</Label>
                <Input
                  id="telemetrySeconds"
                  type="number"
                  min={10}
                  max={3600}
                  value={telemetrySeconds}
                  onChange={(event) => setTelemetrySeconds(Number(event.target.value))}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="driftSeconds">配置漂移检测（秒）</Label>
                <Input
                  id="driftSeconds"
                  type="number"
                  min={5}
                  max={3600}
                  value={driftSeconds}
                  onChange={(event) => setDriftSeconds(Number(event.target.value))}
                />
              </div>
            </div>
            <p className="text-xs text-muted-foreground">当前期望 revision：{settings.agent.revision}</p>
          </CardContent>
        </Card>

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
            <CardTitle>日志缓存</CardTitle>
            <CardDescription>
              操作日志与业务数据隔离存储；请求日志按行追加到分段 JSONL 文件。
              修改后立即清理超出限制的最旧记录。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="operationLogLimit">操作日志保留条数</Label>
              <Input
                id="operationLogLimit"
                type="number"
                min={100}
                max={100000}
                value={operationLogLimit}
                onChange={(event) => setOperationLogLimit(Number(event.target.value))}
              />
              <p className="text-xs text-muted-foreground">
                范围 100-100000，默认 1000 条；超过后按时间删除最旧记录。
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="requestLogMaxMB">请求日志缓存（MB）</Label>
              <Input
                id="requestLogMaxMB"
                type="number"
                min={1}
                max={1024}
                value={requestLogMaxMB}
                onChange={(event) => setRequestLogMaxMB(Number(event.target.value))}
              />
              <p className="text-xs text-muted-foreground">
                范围 1-1024 MB，默认 10 MB；当前占用 {formatBytes(settings.request_log_usage_bytes)}，
                分段文件位于 <code className="rounded bg-muted px-1">{settings.log_dir}</code>。
                {settings.request_log_dropped > 0
                  ? ` 极端负载下累计丢弃 ${settings.request_log_dropped} 条。`
                  : ''}
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>面板证书（TLS）</CardTitle>
            <CardDescription>
              当前访问：
              <Badge variant="outline" className="mx-1">
                {accessProtocol}
              </Badge>
              ；面板监听：
              <Badge variant="outline" className="mx-1">
                {RUNNING_MODE_LABEL[settings.running_tls_mode] ?? settings.running_tls_mode}
              </Badge>
              ；TLS 设置在<strong>重启面板进程后生效</strong>。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {settings.restart_required && (
              <div className="flex items-center justify-between gap-2 rounded-lg border border-yellow-200 bg-yellow-50 px-3 py-2 text-sm text-yellow-700">
                <span>已保存的 TLS 设置与面板当前监听模式不一致，重启面板进程后生效。</span>
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

        <Card>
          <CardHeader>
            <CardTitle>告警</CardTitle>
            <CardDescription>
              服务器离线、配置漂移、节点失败时推送通知（仅状态跃迁触发，同一服务器同一事件 5 分钟内不重复）。
              三项全空 = 关闭；测试使用<strong>已保存</strong>的配置，请先保存。
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="alertWebhook">Webhook 地址</Label>
              <Input
                id="alertWebhook"
                value={alertWebhook}
                onChange={(e) => setAlertWebhook(e.target.value)}
                placeholder="https://example.com/hook，POST JSON"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="alertBotToken">Telegram Bot Token</Label>
              <Input
                id="alertBotToken"
                type="password"
                value={alertBotToken}
                onChange={(e) => setAlertBotToken(e.target.value)}
                placeholder={settings.alert_telegram_bot_token_set ? '已保存，留空保持不变' : 'BotFather 签发的 token'}
                autoComplete="off"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="alertChatID">Telegram Chat ID</Label>
              <Input
                id="alertChatID"
                value={alertChatID}
                onChange={(e) => setAlertChatID(e.target.value)}
                placeholder="与 bot 对话后由 getUpdates 获取"
              />
              <p className="text-xs text-muted-foreground">
                Telegram 通道需 token 与 chat_id 同时具备才发送。
              </p>
            </div>
            <div className="space-y-2">
              <Button type="button" variant="outline" size="sm" disabled={testingAlerts} onClick={onTestAlerts}>
                {testingAlerts ? '发送中…' : '发送测试'}
              </Button>
              {alertTestError && <p className="text-sm text-destructive">{alertTestError}</p>}
              {alertTestResult && (
                <div className="space-y-1 text-sm">
                  {(['webhook', 'telegram'] as const).map((ch) => {
                    const r = alertTestResult[ch]
                    return (
                      <p key={ch} className={r.configured && r.ok ? 'text-green-700' : 'text-destructive'}>
                        {ch === 'webhook' ? 'Webhook' : 'Telegram'}：
                        {!r.configured ? '未配置' : r.ok ? '发送成功' : `发送失败${r.error ? `（${r.error}）` : ''}`}
                      </p>
                    )
                  })}
                </div>
              )}
            </div>
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
          <CardTitle>面板更新</CardTitle>
          <CardDescription>
            以 GitHub release 最新版本为标准检测更新；更新过程中面板操作将被锁定，
            下载/校验/解压/替换进度实时可见，完成后自动重启生效。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted-foreground">当前版本</span>
            <Badge variant="secondary">{settings.panel_version}</Badge>
            {versionInfo && versionInfo.latest && (
              <>
                <span className="text-muted-foreground">最新版本</span>
                <Badge variant={versionInfo.update_available ? 'default' : 'outline'}>
                  {versionInfo.latest}
                </Badge>
              </>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              disabled={checkingUpdate || startingUpdate}
              onClick={onCheckUpdate}
            >
              {checkingUpdate ? '检查中…' : '检查更新'}
            </Button>
            {versionInfo?.update_available && (
              <Button disabled={startingUpdate} onClick={onStartUpdate}>
                {startingUpdate ? '启动中…' : `更新到 ${versionInfo.latest}`}
              </Button>
            )}
          </div>
          {updateError && <p className="text-sm text-destructive">{updateError}</p>}
          {versionInfo && !versionInfo.update_available && !updateError && (
            <p className="text-sm text-muted-foreground">
              {versionInfo.message || '已是最新版本'}
            </p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>面板维护</CardTitle>
          <CardDescription>
            重启面板进程：TLS 等重启生效项、以及后续面板版本更新都经此生效。
            Docker 模式由容器 restart policy 拉起，原生安装由 systemd 拉起；
            仅非托管运行时由面板自派生新进程接管。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-2">
          <Button variant="outline" disabled={restarting} onClick={onRestart}>
            {restarting ? '重启中，请稍候…' : '重启面板'}
          </Button>
              <Button
                variant="outline"
                onClick={() => void api.downloadBackup().catch((err) => setError(errorMessage(err)))}
              >
            下载备份
          </Button>
          <p className="text-xs text-muted-foreground">
            备份为业务 SQLite 数据库快照（VACUUM INTO），可直接替换数据文件恢复；
            <strong>不包含操作日志和请求日志</strong>。
          </p>
        </CardContent>
      </Card>
    </div>
  )
}
