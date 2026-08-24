import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import {
  BellIcon,
  CalendarClockIcon,
  CoinsIcon,
  CpuIcon,
  DatabaseBackupIcon,
  GlobeIcon,
  KeyRoundIcon,
  PlusIcon,
  RefreshCwIcon,
  RocketIcon,
  RssIcon,
  ScrollTextIcon,
  SearchIcon,
  ServerIcon,
  Settings2Icon,
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  Trash2Icon,
  WrenchIcon,
  type LucideIcon,
} from 'lucide-react'

import { LoadingState, Notice, Page } from '@/components/PagePrimitives'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { CURRENCIES, formatBytes, formatDateTime } from '@/lib/format'
import { useTimezone } from '@/lib/timezone'
import type {
  AlertTestResult,
  ExchangeRateSettings,
  InspectionUnit,
  LogSeverity,
  PanelSettings,
  PanelVersionInfo,
} from '@/lib/types'
import { cn } from '@/lib/utils'

import './settings.css'

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
  { value: 'acme', label: "ACME 自动证书（Let's Encrypt）" },
  { value: 'path', label: '域名路径（外部 ACME 证书目录）' },
]

const RUNNING_MODE_LABEL: Record<string, string> = {
  off: 'HTTP',
  cert: 'HTTPS（自带证书）',
  acme: 'HTTPS（ACME）',
  path: 'HTTPS（域名路径）',
}

const INSPECTION_UNITS: { value: InspectionUnit; label: string }[] = [
  { value: 'minute', label: '分钟' },
  { value: 'hour', label: '小时' },
  { value: 'day', label: '天' },
  { value: 'month', label: '月' },
  { value: 'year', label: '年' },
]

// 设置分区（横向 cg-pill 按钮组，active = lime）
const SETTINGS_TABS = [
  { value: 'agent', label: 'Agent', icon: CpuIcon },
  { value: 'runtime', label: '运行设置', icon: SlidersHorizontalIcon },
  { value: 'security', label: '安全通知', icon: ShieldCheckIcon },
  { value: 'system', label: '系统维护', icon: WrenchIcon },
] as const

// 设置分区卡片：左列描述/图标，右列实际配置控件（纯展示组件，无逻辑）。
function SettingsCard({
  icon: Icon,
  tag,
  title,
  description,
  aside,
  children,
}: {
  icon: LucideIcon
  tag: string
  title: ReactNode
  description?: ReactNode
  aside?: ReactNode
  children: ReactNode
}) {
  return (
    <section className="cg-card cg-set-card">
      <aside className="cg-set-card-aside">
        <span className="cg-set-card-icon">
          <Icon />
        </span>
        <span className="cg-micro cg-set-card-tag">{tag}</span>
        <h2 className="cg-title cg-set-card-title">{title}</h2>
        {description ? <p className="cg-set-card-desc">{description}</p> : null}
        {aside}
      </aside>
      <div className="cg-set-card-body">{children}</div>
    </section>
  )
}

export default function Settings() {
  const { refresh: refreshTimezone } = useTimezone()
  const { confirm } = useAppDialog()
  const accessProtocol = window.location.protocol === 'https:' ? 'HTTPS' : 'HTTP'
  const [settings, setSettings] = useState<PanelSettings | null>(null)
  const [loadError, setLoadError] = useState('')
  const [settingsTab, setSettingsTab] = useState('agent')

  // 基本设置
  const [publicURL, setPublicURL] = useState('')
  const [panelShort, setPanelShort] = useState('')
  const [trustedProxies, setTrustedProxies] = useState('')
  const [timezone, setTimezone] = useState('')
  const [trafficTimezone, setTrafficTimezone] = useState('Asia/Shanghai')
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
  const [requestLogLevel, setRequestLogLevel] = useState<LogSeverity>('debug')
  // Agent（面板统一下发）
  const [reconnectMode, setReconnectMode] = useState<'infinite' | 'limited'>('infinite')
  const [reconnectMaxRetries, setReconnectMaxRetries] = useState(10)
  const [telemetrySeconds, setTelemetrySeconds] = useState(60)
  const [driftSeconds, setDriftSeconds] = useState(15)
  const [agentInspectionEvery, setAgentInspectionEvery] = useState(1)
  const [agentInspectionUnit, setAgentInspectionUnit] = useState<InspectionUnit>('day')
  const [agentInspectionAt, setAgentInspectionAt] = useState('03:00')
  const [xrayInspectionEvery, setXrayInspectionEvery] = useState(1)
  const [xrayInspectionUnit, setXrayInspectionUnit] = useState<InspectionUnit>('day')
  const [xrayInspectionAt, setXrayInspectionAt] = useState('03:00')
  const [billingInspectionAt, setBillingInspectionAt] = useState('00:05')
  const [exchangeInspectionAt, setExchangeInspectionAt] = useState('02:30')
  const [reportingCurrency, setReportingCurrency] = useState('CNY')
  const [serverXrayVersion, setServerXrayVersion] = useState('latest')
  const [xrayVersions, setXrayVersions] = useState<string[]>(['latest'])
  const [exchangeData, setExchangeData] = useState<ExchangeRateSettings | null>(null)
  const [customSource, setCustomSource] = useState('')
  const [customSourceAmount, setCustomSourceAmount] = useState('1')
  const [customTargetAmount, setCustomTargetAmount] = useState('')
  const [customBaseSide, setCustomBaseSide] = useState<'source' | 'target'>('source')
  const [refreshingRates, setRefreshingRates] = useState(false)
  const [publicRatesOpen, setPublicRatesOpen] = useState(false)
  const [loadingPublicRates, setLoadingPublicRates] = useState(false)
  const [publicRatesError, setPublicRatesError] = useState('')
  const [deletingCustomRateID, setDeletingCustomRateID] = useState<number | null>(null)
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
  // 订阅设置
  const [subTitle, setSubTitle] = useState('')
  const [subAnnouncement, setSubAnnouncement] = useState('')
  const [subCustomCSS, setSubCustomCSS] = useState('')
  const [subUpdateInterval, setSubUpdateInterval] = useState(24)
  const [subHistoryKeep, setSubHistoryKeep] = useState(6)
  const [subPlanName, setSubPlanName] = useState('')
  const [subAppURL, setSubAppURL] = useState('')
  const [clientCacheTTL, setClientCacheTTL] = useState(72)
  const [savingSub, setSavingSub] = useState(false)
  const [subMessage, setSubMessage] = useState('')
  const [subError, setSubError] = useState('')

  const certFileRef = useRef<HTMLInputElement>(null)
  const keyFileRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    api
      .settings()
      .then((s) => {
        setSettings(s)
        setPublicURL(s.public_url)
        setPanelShort(s.panel_short)
        setTrustedProxies(s.trusted_proxies ?? '')
        setTimezone(s.timezone)
        setTrafficTimezone(s.traffic_timezone)
        setTlsMode(s.tls_mode === '' ? 'flag' : s.tls_mode)
        setTlsDomain(s.tls_domain)
        setAcmeDomain(s.acme_domain)
        setAcmeEmail(s.acme_email)
        setAlertWebhook(s.alert_webhook_url)
        setAlertChatID(s.alert_telegram_chat_id)
        setOperationLogLimit(s.operation_log_limit)
        setRequestLogMaxMB(s.request_log_max_mb)
        setRequestLogLevel(s.request_log_level)
        setReconnectMode(s.agent.reconnect.mode)
        setReconnectMaxRetries(s.agent.reconnect.max_retries)
        setTelemetrySeconds(s.agent.telemetry.interval_seconds)
        setDriftSeconds(s.agent.drift_detection.interval_seconds)
        setAgentInspectionEvery(s.release_inspection.agent.every)
        setAgentInspectionUnit(s.release_inspection.agent.unit)
        setAgentInspectionAt(s.release_inspection.agent.at ?? '03:00')
        setXrayInspectionEvery(s.release_inspection.xray.every)
        setXrayInspectionUnit(s.release_inspection.xray.unit)
        setXrayInspectionAt(s.release_inspection.xray.at ?? '03:00')
        setBillingInspectionAt(s.billing_inspection.at ?? '00:05')
        setExchangeInspectionAt(s.exchange_rate_inspection.at ?? '02:30')
        setReportingCurrency(s.reporting_currency)
        setServerXrayVersion(s.server_settings.xray_version ?? 'latest')
      })
      .catch((err) => setLoadError(errorMessage(err)))
  }, [])

  useEffect(() => {
    api
      .exchangeRates()
      .then(setExchangeData)
      .catch(() => {})
  }, [])

  useEffect(() => {
    api
      .releaseVersions('xray')
      .then((versions) => {
        setXrayVersions(versions.versions)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    api
      .subSettings()
      .then((s) => {
        setSubTitle(s.title)
        setSubAnnouncement(s.announcement)
        setSubCustomCSS(s.custom_css)
        setSubUpdateInterval(s.update_interval)
        setSubHistoryKeep(s.traffic_history_keep)
        setSubPlanName(s.plan_name)
        setSubAppURL(s.app_url)
        setClientCacheTTL(s.client_cache_ttl_hours)
      })
      .catch(() => {})
  }, [])

  const onSaveSub = async () => {
    setSavingSub(true)
    setSubError('')
    setSubMessage('')
    try {
      await api.updateSubSettings({
        title: subTitle,
        announcement: subAnnouncement,
        custom_css: subCustomCSS,
        update_interval: subUpdateInterval,
        traffic_history_keep: subHistoryKeep,
        plan_name: subPlanName,
        app_url: subAppURL,
        client_cache_ttl_hours: clientCacheTTL,
      })
      setSubMessage('已保存。')
    } catch (err) {
      setSubError(errorMessage(err))
    } finally {
      setSavingSub(false)
    }
  }

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
        traffic_timezone: trafficTimezone.trim(),
        public_url: publicURL.trim(),
        panel_short: panelShort.trim(),
        trusted_proxies: trustedProxies.trim(),
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
        request_log_level: requestLogLevel,
        agent: {
          revision: settings?.agent.revision ?? 1,
          reconnect: {
            mode: reconnectMode,
            max_retries: reconnectMaxRetries,
          },
          telemetry: { interval_seconds: telemetrySeconds },
          drift_detection: { interval_seconds: driftSeconds },
        },
        release_inspection: {
          agent: {
            every: agentInspectionEvery,
            unit: agentInspectionUnit,
            ...(agentInspectionUnit === 'minute' || agentInspectionUnit === 'hour'
              ? {}
              : { at: agentInspectionAt }),
          },
          xray: {
            every: xrayInspectionEvery,
            unit: xrayInspectionUnit,
            ...(xrayInspectionUnit === 'minute' || xrayInspectionUnit === 'hour'
              ? {}
              : { at: xrayInspectionAt }),
          },
        },
        billing_inspection: { every: 1, unit: 'day', at: billingInspectionAt },
        exchange_rate_inspection: { every: 1, unit: 'day', at: exchangeInspectionAt },
        reporting_currency: reportingCurrency,
        server_settings: { xray_version: serverXrayVersion },
        // bot token 留空 = 保持已保存值（后端语义，与 tls key 一致）
        ...(alertBotToken.trim() ? { alert_telegram_bot_token: alertBotToken.trim() } : {}),
      })
      setSettings(s)
      setExchangeData(await api.exchangeRates())
      setCertPEM('')
      setKeyPEM('')
      refreshTimezone()
      setMessage(
        s.restart_required ? '已保存。TLS/证书设置将在面板进程重启后生效。' : '已保存并生效。',
      )
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  const refreshRates = async () => {
    setRefreshingRates(true)
    try {
      setExchangeData(await api.refreshExchangeRates())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRefreshingRates(false)
    }
  }

  const showPublicRates = async () => {
    setPublicRatesOpen(true)
    setLoadingPublicRates(true)
    setPublicRatesError('')
    try {
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      setPublicRatesError(errorMessage(err))
    } finally {
      setLoadingPublicRates(false)
    }
  }

  const addCustomRate = async () => {
    try {
      await api.saveCustomExchangeRate({
        id: 0,
        source_currency: customSource,
        source_amount: customSourceAmount,
        target_currency: reportingCurrency,
        target_amount: customTargetAmount,
        enabled: true,
      })
      setExchangeData(await api.exchangeRates())
      setCustomSource('')
      setCustomSourceAmount(customBaseSide === 'source' ? '1' : '')
      setCustomTargetAmount('')
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  const changeCustomBaseSide = (side: 'source' | 'target') => {
    setCustomBaseSide(side)
    setCustomSourceAmount(side === 'source' ? '1' : '')
    setCustomTargetAmount(side === 'target' ? '1' : '')
  }

  const setCustomRateEnabled = async (
    rate: ExchangeRateSettings['custom_rates'][number],
    enabled: boolean,
  ) => {
    try {
      await api.saveCustomExchangeRate({ ...rate, enabled })
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      setError(errorMessage(err))
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
    if (
      !(await confirm({
        title: '重启面板',
        description: '确定重启面板进程？重启期间面板会短暂不可用（数秒）。',
        confirmLabel: '重启面板',
      }))
    ) {
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
      !(await confirm({
        title: `更新到 ${target}`,
        description: '更新期间面板操作将被锁定，完成后自动重启（短暂不可用）。',
        confirmLabel: '开始更新',
      }))
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

  // 强制更新：版本号相同时覆盖安装，锚定版本为 GitHub Release 最新版本。
  const onForceUpdate = async () => {
    const target = versionInfo?.latest ?? 'latest'
    if (
      !(await confirm({
        title: '强制覆盖安装',
        description: `当前版本号没有更新，是否强制覆盖安装？\n将重新下载并安装 GitHub Release 的 ${target} 版本，同时向全部 Agent 下发强制更新。\n更新期间面板操作将被锁定，面板和 Agent 将依次重启并重新连接。`,
        confirmLabel: '强制更新',
        destructive: true,
      }))
    ) {
      return
    }
    setStartingUpdate(true)
    setUpdateError('')
    try {
      await api.startPanelUpdate(target, true)
    } catch (err) {
      setUpdateError(errorMessage(err))
    } finally {
      setStartingUpdate(false)
    }
  }

  if (loadError) {
    return (
      <Page className="page-shell-narrow">
        <Notice tone="danger">{loadError}</Notice>
      </Page>
    )
  }

  const deleteCustomRate = async (id: number) => {
    setDeletingCustomRateID(id)
    setError('')
    try {
      await api.deleteCustomExchangeRate(id)
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeletingCustomRateID(null)
    }
  }
  if (!settings) {
    return (
      <Page className="page-shell-narrow">
        <LoadingState />
      </Page>
    )
  }

  const configuredSources = new Set(
    (exchangeData?.custom_rates ?? []).map((rate) => rate.source_currency),
  )
  const customSourceOptions = CURRENCIES.filter(
    (currency) => currency !== reportingCurrency && !configuredSources.has(currency),
  )
  const reportingCurrencyPending = reportingCurrency !== settings.reporting_currency
  const customRateReady = Boolean(
    customSource &&
    customSourceAmount &&
    customTargetAmount &&
    (Number(customSourceAmount) === 1 || Number(customTargetAmount) === 1) &&
    !reportingCurrencyPending,
  )

  return (
    <Page className="cg-settings">
      {/* Eyebrow + 环境信息 */}
      <div className="cg-set-topline">
        <span className="cg-eyebrow">PANEL / SETTINGS</span>
        <div className="cg-set-topline-side">
          <span className="cg-pill">{accessProtocol} 访问</span>
          <span className="cg-pill is-dark">v{settings.panel_version}</span>
        </div>
      </div>

      {/* Header Card */}
      <header className="cg-card-raised cg-set-header">
        <div className="cg-set-header-main">
          <span className="cg-set-header-icon">
            <Settings2Icon />
          </span>
          <div className="min-w-0">
            <span className="cg-micro cg-set-header-tag">CONFIGURATION / 全局配置</span>
            <h1 className="cg-title cg-set-title">设置</h1>
            <p className="cg-set-subtitle">
              面板、Agent、订阅与安全的全局配置集中管理；保存后在线 Agent 立即同步，离线 Agent
              重连后拉取。
            </p>
          </div>
        </div>
        <div className="cg-set-header-side">
          <span className={cn('cg-status', settings.restart_required ? 'is-red' : 'is-lime')}>
            {settings.restart_required ? 'RESTART REQUIRED' : 'CONFIG APPLIED'}
          </span>
        </div>
      </header>

      {/* 横向 Category Tabs（cg-pill 按钮组，active = lime） */}
      <nav className="cg-set-tabs" aria-label="设置分类">
        {SETTINGS_TABS.map((tab) => (
          <button
            key={tab.value}
            type="button"
            className={cn('cg-pill cg-set-tab', settingsTab === tab.value && 'is-active')}
            aria-pressed={settingsTab === tab.value}
            onClick={() => setSettingsTab(tab.value)}
          >
            <tab.icon />
            {tab.label}
          </button>
        ))}
      </nav>

      {error && <Notice tone="danger">{error}</Notice>}
      {message && <Notice tone="success">{message}</Notice>}

      <form onSubmit={onSave} className="cg-set-form">
        {/* Agent 分区 */}
        <div className="cg-set-panel" hidden={settingsTab !== 'agent'}>
          <SettingsCard
            icon={CpuIcon}
            tag="AGENT / POLICY"
            title="Agent"
            description="所有 Agent 使用同一份设置。保存后 revision 自动递增，在线 Agent 会立即拉取，离线 Agent 在重连后同步。"
            aside={<span className="cg-status is-blue">REVISION {settings.agent.revision}</span>}
          >
            <div className="flex flex-col gap-2">
              <Label>重连策略</Label>
              <Select
                value={reconnectMode}
                onValueChange={(value) =>
                  value && setReconnectMode(value as 'infinite' | 'limited')
                }
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
              <p className="cg-set-note">
                两种策略均使用指数退避。限制次数耗尽或认证失败后，Agent 仍会每 5
                分钟低频探测，不会永久停止。
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
              <p className="cg-set-note">范围 1-100；无限重试模式会保存但忽略该值。</p>
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
          </SettingsCard>

          <SettingsCard
            icon={ServerIcon}
            tag="SERVER / DEFAULTS"
            title="服务器设置"
            description="面板级默认设置（defaultsetting）。服务器未单独覆盖时采用该值；xray 版本为具体版本时，agent 收到后会自动对齐升级到该版本；latest 保持现状（仅手动升级）。"
            aside={
              <span className="cg-status is-blue">
                REV {settings?.server_settings_revision ?? 1}
              </span>
            }
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="serverXrayVersion">xray 版本（默认）</Label>
              <Select
                value={serverXrayVersion}
                onValueChange={(value) => value && setServerXrayVersion(value)}
                items={xrayVersions.map((version) => ({ value: version, label: version }))}
              >
                <SelectTrigger id="serverXrayVersion" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {xrayVersions.map((version) => (
                    <SelectItem key={version} value={version}>
                      {version}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="cg-set-note">版本列表来自 GitHub release 缓存。</p>
            </div>
          </SettingsCard>

          <SettingsCard
            icon={CalendarClockIcon}
            tag="INSPECTION / SCHEDULE"
            title="巡检任务"
            description="定期从 GitHub 更新升级弹窗使用的 release 版本缓存。GitHub 暂时不可用时继续使用最近一次成功缓存。"
          >
            <div className="cg-set-group">
              <span className="cg-set-group-title">Agent 版本</span>
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)]">
                <div className="flex flex-col gap-2">
                  <Label htmlFor="agentInspectionEvery" className="cg-set-sublabel">
                    每隔
                  </Label>
                  <Input
                    id="agentInspectionEvery"
                    type="number"
                    min={1}
                    max={10000}
                    value={agentInspectionEvery}
                    onChange={(event) => setAgentInspectionEvery(Number(event.target.value))}
                  />
                </div>
                <div className="flex flex-col gap-2">
                  <Label className="cg-set-sublabel">单位</Label>
                  <Select
                    value={agentInspectionUnit}
                    onValueChange={(value) =>
                      value && setAgentInspectionUnit(value as InspectionUnit)
                    }
                    items={INSPECTION_UNITS}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {INSPECTION_UNITS.map((unit) => (
                        <SelectItem key={unit.value} value={unit.value}>
                          {unit.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                {agentInspectionUnit !== 'minute' && agentInspectionUnit !== 'hour' && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="agentInspectionAt" className="cg-set-sublabel">
                      执行时间
                    </Label>
                    <Input
                      id="agentInspectionAt"
                      type="time"
                      value={agentInspectionAt}
                      onChange={(event) => setAgentInspectionAt(event.target.value)}
                    />
                  </div>
                )}
              </div>
            </div>
            <div className="cg-set-divider" />
            <div className="cg-set-group">
              <span className="cg-set-group-title">xray 版本</span>
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)]">
                <div className="flex flex-col gap-2">
                  <Label htmlFor="xrayInspectionEvery" className="cg-set-sublabel">
                    每隔
                  </Label>
                  <Input
                    id="xrayInspectionEvery"
                    type="number"
                    min={1}
                    max={10000}
                    value={xrayInspectionEvery}
                    onChange={(event) => setXrayInspectionEvery(Number(event.target.value))}
                  />
                </div>
                <div className="flex flex-col gap-2">
                  <Label className="cg-set-sublabel">单位</Label>
                  <Select
                    value={xrayInspectionUnit}
                    onValueChange={(value) =>
                      value && setXrayInspectionUnit(value as InspectionUnit)
                    }
                    items={INSPECTION_UNITS}
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {INSPECTION_UNITS.map((unit) => (
                        <SelectItem key={unit.value} value={unit.value}>
                          {unit.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                {xrayInspectionUnit !== 'minute' && xrayInspectionUnit !== 'hour' && (
                  <div className="flex flex-col gap-2">
                    <Label htmlFor="xrayInspectionAt" className="cg-set-sublabel">
                      执行时间
                    </Label>
                    <Input
                      id="xrayInspectionAt"
                      type="time"
                      value={xrayInspectionAt}
                      onChange={(event) => setXrayInspectionAt(event.target.value)}
                    />
                  </div>
                )}
              </div>
              <p className="cg-set-note">默认每天 03:00 巡检一次。</p>
            </div>
            <div className="cg-set-divider" />
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label htmlFor="billingInspectionAt">计费状态巡检</Label>
                <Input
                  id="billingInspectionAt"
                  type="time"
                  value={billingInspectionAt}
                  onChange={(e) => setBillingInspectionAt(e.target.value)}
                />
                <p className="cg-set-note">每日执行并驱动服务器计费状态机。</p>
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="exchangeInspectionAt">汇率刷新</Label>
                <Input
                  id="exchangeInspectionAt"
                  type="time"
                  value={exchangeInspectionAt}
                  onChange={(e) => setExchangeInspectionAt(e.target.value)}
                />
                <p className="cg-set-note">每日从 Frankfurter 更新持久化缓存。</p>
              </div>
            </div>
          </SettingsCard>
        </div>

        {/* 运行设置分区 */}
        <div className="cg-set-panel" hidden={settingsTab !== 'runtime'}>
          <SettingsCard
            icon={CoinsIcon}
            tag="BILLING / EXCHANGE"
            title="费用换算"
            description="服务器保留原价和原币种，汇总及详情按统计币种折算。"
          >
            <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
              <div className="flex flex-col gap-2">
                <Label>统计币种</Label>
                <Select
                  value={reportingCurrency}
                  onValueChange={(v) => v && setReportingCurrency(v)}
                  items={CURRENCIES.map((c) => ({ value: c, label: c }))}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {CURRENCIES.map((c) => (
                      <SelectItem key={c} value={c}>
                        {c}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-wrap gap-2 self-end">
                <Button type="button" variant="outline" onClick={() => void showPublicRates()}>
                  <SearchIcon />
                  公开汇率查询
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  disabled={refreshingRates}
                  onClick={refreshRates}
                >
                  <RefreshCwIcon className={refreshingRates ? 'animate-spin' : ''} />
                  {refreshingRates ? '刷新中…' : '立即刷新汇率'}
                </Button>
              </div>
            </div>
            <div className="cg-set-divider" />
            <div className="cg-set-group">
              <div className="cg-set-inline-row">
                <Label>自定义汇率</Label>
                <div className="cg-set-segment" role="group" aria-label="自定义汇率基准侧">
                  <button
                    type="button"
                    className={cn(customBaseSide === 'source' && 'is-active')}
                    onClick={() => changeCustomBaseSide('source')}
                  >
                    源币种 = 1
                  </button>
                  <button
                    type="button"
                    className={cn(customBaseSide === 'target' && 'is-active')}
                    onClick={() => changeCustomBaseSide('target')}
                  >
                    展示币种 = 1
                  </button>
                </div>
              </div>
              <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] sm:items-center">
                <div className="grid grid-cols-[minmax(0,1fr)_96px] gap-2">
                  <Input
                    type="number"
                    min="0"
                    step="any"
                    placeholder="金额"
                    readOnly={customBaseSide === 'source'}
                    value={customSourceAmount}
                    onChange={(e) => setCustomSourceAmount(e.target.value)}
                    aria-label="源币种金额"
                  />
                  <Select
                    value={customSource}
                    onValueChange={(v) => v && setCustomSource(v)}
                    items={customSourceOptions.map((c) => ({ value: c, label: c }))}
                  >
                    <SelectTrigger aria-label="源币种">
                      <SelectValue placeholder="币种" />
                    </SelectTrigger>
                    <SelectContent>
                      {customSourceOptions.map((c) => (
                        <SelectItem key={c} value={c}>
                          {c}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <span className="cg-set-ratio">:</span>
                <div className="grid grid-cols-[minmax(0,1fr)_96px] gap-2">
                  <Input
                    type="number"
                    min="0"
                    step="any"
                    placeholder="金额"
                    readOnly={customBaseSide === 'target'}
                    value={customTargetAmount}
                    onChange={(e) => setCustomTargetAmount(e.target.value)}
                    aria-label="展示币种金额"
                  />
                  <div className="cg-set-static-field" aria-label="展示币种">
                    {reportingCurrency}
                  </div>
                </div>
              </div>
              <div>
                <button
                  type="button"
                  className="cg-button is-primary"
                  disabled={!customRateReady}
                  onClick={addCustomRate}
                >
                  <PlusIcon />
                  保存并启用
                </button>
              </div>
              {reportingCurrencyPending ? (
                <p className="cg-set-msg-info">展示币种已修改，请先保存设置再添加自定义汇率。</p>
              ) : null}
              <p className="cg-set-note">
                两侧必须有一侧为 1。以 1 USD : 7 CNY 为例：USD 直接按该汇率换算；CAD、EUR、JPY
                等先按 Frankfurter 换成 USD，再按自定义汇率换成 CNY；原价为 CNY
                的费用保持不变。切换展示币种不会删除记录，仅目标币种匹配当前展示币种的启用项参与自定义结果。费用详情同时显示公共汇率与自定义汇率结果。
              </p>
            </div>
            <div className="cg-set-divider" />
            <div className="cg-set-rate-list">
              {(exchangeData?.custom_rates ?? []).map((rate) => (
                <div key={rate.id} className="cg-set-rate-row">
                  <div className="cg-set-rate-info">
                    <span className="cg-set-rate-text">
                      {rate.source_amount} {rate.source_currency} : {rate.target_amount}{' '}
                      {rate.target_currency}
                    </span>
                    <span
                      className={cn(
                        'cg-status',
                        rate.enabled && rate.target_currency === reportingCurrency
                          ? 'is-lime'
                          : rate.enabled
                            ? 'is-blue'
                            : 'is-muted',
                      )}
                    >
                      {rate.enabled && rate.target_currency === reportingCurrency
                        ? '当前使用'
                        : rate.enabled
                          ? `未应用 · ${rate.target_currency}`
                          : '已停用'}
                    </span>
                  </div>
                  <div className="cg-set-rate-actions">
                    <Button
                      type="button"
                      variant={rate.enabled ? 'secondary' : 'outline'}
                      size="sm"
                      onClick={() => setCustomRateEnabled(rate, !rate.enabled)}
                    >
                      {rate.enabled ? '停用' : '启用'}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      className="cg-set-danger-btn"
                      aria-label="删除自定义汇率"
                      disabled={deletingCustomRateID === rate.id}
                      onClick={() => void deleteCustomRate(rate.id)}
                    >
                      <Trash2Icon />
                    </Button>
                  </div>
                </div>
              ))}
              <p className="cg-set-note">
                公开汇率日期：{exchangeData?.rates[0]?.rate_date || '暂无缓存'}
              </p>
            </div>
          </SettingsCard>

          <SettingsCard
            icon={GlobeIcon}
            tag="PANEL / GENERAL"
            title="基本设置"
            description="对外地址与时区保存后立即生效。"
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="publicURL">面板对外地址（含协议与端口）</Label>
              <Input
                id="publicURL"
                value={publicURL}
                onChange={(e) => setPublicURL(e.target.value)}
                placeholder="https://panel.example.com:8443，留空按请求推断"
              />
              <p className="cg-set-note">
                用于生成 agent 安装命令与订阅链接；反代部署时填反代后的地址（https 即安全入口）。
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="panelShort">面板缩写</Label>
              <Input
                id="panelShort"
                value={panelShort}
                onChange={(e) => setPanelShort(e.target.value)}
                placeholder="Lattix"
              />
              <p className="cg-set-note">
                用于链路命名模板变量 {'{{PANEL_SHORT}}'} 与订阅的「面板缩写 分组」策略组名；
                留空恢复默认 Lattix，保存后新生成的订阅与链路名称生效。
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="trustedProxies">可信反代网段（CIDR，逗号分隔）</Label>
              <Input
                id="trustedProxies"
                value={trustedProxies}
                onChange={(e) => setTrustedProxies(e.target.value)}
                placeholder="173.245.48.0/20 等公网回源网段，通常留空"
              />
              <p className="cg-set-note">
                本机回环与内网/容器网段（10/8、172.16/12、192.168/16、100.64/10 等）默认可信，
                1panel/OpenResty、docker、局域网 nginx 反代的 X-Forwarded-Proto/For 无需配置即被采纳
                （安装命令协议、订阅链接与日志 IP 随之正确）；此处仅用于追加公网回源网段（如 CDN）。
              </p>
            </div>
            <div className="flex flex-col gap-2">
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
              <p className="cg-set-note">
                IANA 时区名（如 Asia/Shanghai），全局生效：所有浏览器看到的面板时间一致。
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="trafficTimezone">流量统计时区</Label>
              <Input
                id="trafficTimezone"
                list="timezone-list"
                value={trafficTimezone}
                onChange={(e) => setTrafficTimezone(e.target.value)}
                placeholder="Asia/Shanghai"
                required
              />
            </div>
          </SettingsCard>

          <SettingsCard
            icon={RssIcon}
            tag="SUBSCRIPTION / PORTAL"
            title="订阅"
            description="订阅落地页与客户端流量信息全局配置。用户级覆盖在用户页“订阅设置”中配置。"
          >
            <div className="flex flex-col gap-2">
              <Label>落地页标题</Label>
              <Input
                value={subTitle}
                onChange={(e) => setSubTitle(e.target.value)}
                placeholder="Lattix 订阅"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label>公告（Markdown）</Label>
              <textarea
                className="cg-set-textarea"
                rows={4}
                value={subAnnouncement}
                onChange={(e) => setSubAnnouncement(e.target.value)}
                placeholder="支持 Markdown 格式，留空则不显示公告区域"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label>自定义 CSS</Label>
              <textarea
                className="cg-set-textarea"
                rows={3}
                value={subCustomCSS}
                onChange={(e) => setSubCustomCSS(e.target.value)}
                placeholder="注入到订阅落地页的额外样式"
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label>默认更新间隔（小时）</Label>
                <Input
                  type="number"
                  min={1}
                  max={720}
                  value={subUpdateInterval}
                  onChange={(e) => setSubUpdateInterval(Number(e.target.value) || 24)}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>流量历史保留周期数</Label>
                <Input
                  type="number"
                  min={1}
                  max={60}
                  value={subHistoryKeep}
                  onChange={(e) => setSubHistoryKeep(Number(e.target.value) || 6)}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>客户端安装包缓存（小时）</Label>
                <Input
                  type="number"
                  min={1}
                  max={720}
                  value={clientCacheTTL}
                  onChange={(e) => setClientCacheTTL(Number(e.target.value) || 72)}
                />
                <p className="cg-set-note">默认 72 小时。每个客户端与架构只保留最新版本。</p>
              </div>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label>默认套餐名</Label>
                <Input
                  value={subPlanName}
                  onChange={(e) => setSubPlanName(e.target.value)}
                  placeholder="如 VIP1，客户端 hover 流量信息时显示"
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>默认跳转链接</Label>
                <Input
                  value={subAppURL}
                  onChange={(e) => setSubAppURL(e.target.value)}
                  placeholder="客户端流量卡片可点击跳转的按钮 URL"
                />
              </div>
            </div>
            <div className="cg-set-actions">
              <button
                type="button"
                className="cg-button is-primary"
                disabled={savingSub}
                onClick={onSaveSub}
              >
                {savingSub ? '保存中…' : '保存订阅设置'}
              </button>
              {subMessage && <span className="cg-set-msg-ok">{subMessage}</span>}
              {subError && <span className="cg-set-msg-err">{subError}</span>}
            </div>
          </SettingsCard>

          <SettingsCard
            icon={ScrollTextIcon}
            tag="LOGGING / RETENTION"
            title="日志缓存"
            description="操作日志与业务数据隔离存储；请求日志按行追加到分段 JSONL 文件。修改后立即清理超出限制的最旧记录。"
          >
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
              <p className="cg-set-note">
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
              <p className="cg-set-note">
                范围 1-1024 MB，默认 10 MB；当前占用 {formatBytes(settings.request_log_usage_bytes)}
                ， 分段文件位于 <code>{settings.log_dir}</code>。
                {settings.request_log_dropped > 0
                  ? ` 极端负载下累计丢弃 ${settings.request_log_dropped} 条。`
                  : ''}
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="requestLogLevel">请求日志记录级别</Label>
              <Select
                value={requestLogLevel}
                onValueChange={(value) => setRequestLogLevel((value as LogSeverity) ?? 'debug')}
              >
                <SelectTrigger id="requestLogLevel" className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="debug">调试（记录全部）</SelectItem>
                    <SelectItem value="info">信息（过滤调试）</SelectItem>
                    <SelectItem value="warning">警告（仅警告/错误）</SelectItem>
                    <SelectItem value="error">错误（仅错误）</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
              <p className="cg-set-note">
                低于该级别的请求不写入日志。状态轮询等高频请求记录为调试级，选择"信息"即可过滤。
              </p>
            </div>
          </SettingsCard>
        </div>

        {/* 安全通知分区 */}
        <div className="cg-set-panel" hidden={settingsTab !== 'security'}>
          <SettingsCard
            icon={ShieldCheckIcon}
            tag="SECURITY / TLS"
            title="面板证书（TLS）"
            description="TLS 设置保存后写入配置，重启面板进程后生效；当前访问协议与面板监听模式见下方贴纸。"
            aside={
              <div className="cg-set-facts">
                <span
                  className={cn('cg-status', accessProtocol === 'HTTPS' ? 'is-lime' : 'is-blue')}
                >
                  访问 {accessProtocol}
                </span>
                <span className="cg-status is-muted">
                  监听 {RUNNING_MODE_LABEL[settings.running_tls_mode] ?? settings.running_tls_mode}
                </span>
              </div>
            }
          >
            {settings.restart_required && (
              <Notice
                tone="warning"
                actions={
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={restarting}
                    onClick={onRestart}
                  >
                    {restarting ? '重启中…' : '立即重启'}
                  </Button>
                }
              >
                已保存的 TLS 设置与面板当前监听模式不一致，重启面板进程后生效。
              </Notice>
            )}
            <div className="flex flex-col gap-2">
              <Label>TLS 模式</Label>
              <Select
                value={tlsMode}
                onValueChange={(v) => v && setTlsMode(v as TLSModeChoice)}
                items={TLS_MODES}
              >
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
                  <p className="cg-set-note">
                    已保存证书：CN={settings.tls_cert.common_name || '-'}
                    {(settings.tls_cert.dns_names ?? []).length > 0 &&
                      `，SAN=${(settings.tls_cert.dns_names ?? []).join(', ')}`}
                    ，到期 {formatDateTime(settings.tls_cert.not_after)}
                    {settings.tls_cert.expired && (
                      <span className="cg-set-msg-err">（已过期）</span>
                    )}
                    {settings.tls_key_set && '；私钥已保存'}
                  </p>
                )}
                <div className="flex flex-col gap-2">
                  <div className="cg-set-inline-row">
                    <Label htmlFor="certPEM">证书 PEM（留空保持不变）</Label>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => certFileRef.current?.click()}
                    >
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
                    className="cg-set-textarea"
                    rows={6}
                    value={certPEM}
                    onChange={(e) => setCertPEM(e.target.value)}
                    placeholder="-----BEGIN CERTIFICATE-----"
                  />
                </div>
                <div className="flex flex-col gap-2">
                  <div className="cg-set-inline-row">
                    <Label htmlFor="keyPEM">私钥 PEM（留空保持不变）</Label>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => keyFileRef.current?.click()}
                    >
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
                    className="cg-set-textarea"
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
                <div className="flex flex-col gap-2">
                  <Label htmlFor="tlsDomain">证书域名</Label>
                  <Input
                    id="tlsDomain"
                    value={tlsDomain}
                    onChange={(e) => setTlsDomain(e.target.value)}
                    placeholder="panel.example.com"
                  />
                  <p className="cg-set-note">
                    面板从证书根目录 <code>{settings.tls_dir}</code> 读取
                    <code>{'<域名>/fullchain.pem'}</code> 与<code>{'<域名>/privkey.pem'}</code>
                    （如 <code>{settings.tls_dir}/panel.example.com/fullchain.pem</code>）。 外部
                    ACME（安装脚本）申请/续期后写入该目录即可，续期替换文件后下一次 TLS
                    握手自动加载新证书，无需重启。保存时会校验证书已存在且配对有效。
                  </p>
                </div>
                {settings.tls_cert && (
                  <p className="cg-set-note">
                    目录内当前证书：CN={settings.tls_cert.common_name || '-'}
                    {(settings.tls_cert.dns_names ?? []).length > 0 &&
                      `，SAN=${(settings.tls_cert.dns_names ?? []).join(', ')}`}
                    ，到期 {formatDateTime(settings.tls_cert.not_after)}
                    {settings.tls_cert.expired && (
                      <span className="cg-set-msg-err">（已过期）</span>
                    )}
                  </p>
                )}
              </>
            )}

            {tlsMode === 'acme' && (
              <>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="acmeDomain">ACME 域名</Label>
                  <Input
                    id="acmeDomain"
                    value={acmeDomain}
                    onChange={(e) => setAcmeDomain(e.target.value)}
                    placeholder="panel.example.com"
                  />
                  <p className="cg-set-note">
                    Let&apos;s Encrypt TLS-ALPN-01 签发，需 443 端口公网可达。
                  </p>
                </div>
                <div className="flex flex-col gap-2">
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
          </SettingsCard>

          <SettingsCard
            icon={BellIcon}
            tag="ALERTS / NOTIFY"
            title="告警"
            description="服务器离线、配置漂移、节点失败时推送通知（仅状态跃迁触发，同一服务器同一事件 5 分钟内不重复）。三项全空 = 关闭；测试使用已保存的配置，请先保存。"
          >
            <div className="flex flex-col gap-2">
              <Label htmlFor="alertWebhook">Webhook 地址</Label>
              <Input
                id="alertWebhook"
                value={alertWebhook}
                onChange={(e) => setAlertWebhook(e.target.value)}
                placeholder="https://example.com/hook，POST JSON"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="alertBotToken">Telegram Bot Token</Label>
              <Input
                id="alertBotToken"
                type="password"
                value={alertBotToken}
                onChange={(e) => setAlertBotToken(e.target.value)}
                placeholder={
                  settings.alert_telegram_bot_token_set
                    ? '已保存，留空保持不变'
                    : 'BotFather 签发的 token'
                }
                autoComplete="off"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="alertChatID">Telegram Chat ID</Label>
              <Input
                id="alertChatID"
                value={alertChatID}
                onChange={(e) => setAlertChatID(e.target.value)}
                placeholder="与 bot 对话后由 getUpdates 获取"
              />
              <p className="cg-set-note">Telegram 通道需 token 与 chat_id 同时具备才发送。</p>
            </div>
            <div className="cg-set-group">
              <div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={testingAlerts}
                  onClick={onTestAlerts}
                >
                  {testingAlerts ? '发送中…' : '发送测试'}
                </Button>
              </div>
              {alertTestError && <p className="cg-set-msg-err">{alertTestError}</p>}
              {alertTestResult && (
                <div className="cg-set-alert-results">
                  {(['webhook', 'telegram'] as const).map((ch) => {
                    const r = alertTestResult[ch]
                    return (
                      <div key={ch} className="cg-set-alert-line">
                        <span
                          className={cn(
                            'cg-status',
                            !r.configured ? 'is-muted' : r.ok ? 'is-lime' : 'is-red',
                          )}
                        >
                          {ch === 'webhook' ? 'WEBHOOK' : 'TELEGRAM'}
                        </span>
                        <span className="cg-set-note">
                          {!r.configured
                            ? '未配置'
                            : r.ok
                              ? '发送成功'
                              : `发送失败${r.error ? `（${r.error}）` : ''}`}
                        </span>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          </SettingsCard>
        </div>

        {/* 保存栏：固定在分区底部，lime 主按钮 */}
        {settingsTab !== 'system' && (
          <div className="cg-set-save-bar">
            <span className="cg-micro cg-set-save-hint">SAVE / 保存后全局生效，TLS 变更需重启</span>
            <button type="submit" className="cg-button is-primary" disabled={saving}>
              {saving ? '保存中…' : '保存设置'}
            </button>
          </div>
        )}
      </form>

      {/* 公开汇率 Dialog */}
      <Dialog open={publicRatesOpen} onOpenChange={setPublicRatesOpen}>
        <DialogContent className="max-h-[85vh] sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>公开汇率</DialogTitle>
            <DialogDescription>
              Frankfurter 公开汇率缓存，拉取 EUR / USD / CNY / JPY / CAD 五种基准。
            </DialogDescription>
          </DialogHeader>
          {loadingPublicRates ? (
            <LoadingState />
          ) : publicRatesError ? (
            <Notice tone="danger">{publicRatesError}</Notice>
          ) : exchangeData?.rates.length ? (
            <div className="max-h-[60vh] overflow-auto rounded-lg border">
              <Table>
                <TableHeader className="sticky top-0 z-10 bg-popover">
                  <TableRow>
                    <TableHead>基准币种</TableHead>
                    <TableHead>报价币种</TableHead>
                    <TableHead>公开汇率</TableHead>
                    <TableHead>汇率日期</TableHead>
                    <TableHead>抓取时间</TableHead>
                    <TableHead>来源</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {exchangeData.rates.map((rate) => (
                    <TableRow key={`${rate.base_currency}-${rate.quote_currency}`}>
                      <TableCell className="font-medium">{rate.base_currency}</TableCell>
                      <TableCell className="font-medium">{rate.quote_currency}</TableCell>
                      <TableCell className="tabular-nums">
                        1 {rate.base_currency} = {rate.rate} {rate.quote_currency}
                      </TableCell>
                      <TableCell>{rate.rate_date}</TableCell>
                      <TableCell>{formatDateTime(rate.fetched_at, timezone)}</TableCell>
                      <TableCell className="capitalize">{rate.source}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <p className="cg-set-dialog-empty">暂无公开汇率缓存，请先刷新汇率。</p>
          )}
        </DialogContent>
      </Dialog>

      {/* 系统维护分区（独立表单与操作，不参与上方统一保存） */}
      <div className="cg-set-panel" hidden={settingsTab !== 'system'}>
        <SettingsCard
          icon={KeyRoundIcon}
          tag="ACCOUNT / PASSWORD"
          title="修改密码"
          description={`账号 ${settings.admin_user}${settings.password_override ? '（密码已被设置页覆盖）' : '（当前使用启动参数密码）'}；修改后所有会话失效，需重新登录。`}
        >
          <form onSubmit={onChangePassword} className="cg-set-group">
            <div className="flex flex-col gap-2">
              <Label htmlFor="currentPassword">当前密码</Label>
              <Input
                id="currentPassword"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                autoComplete="current-password"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="newPassword">新密码（至少 8 位）</Label>
              <Input
                id="newPassword"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="confirmPassword">确认新密码</Label>
              <Input
                id="confirmPassword"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoComplete="new-password"
              />
            </div>
            {passwordError && <p className="cg-set-msg-err">{passwordError}</p>}
            {passwordMessage && <p className="cg-set-msg-ok">{passwordMessage}</p>}
            <div>
              <button
                type="submit"
                className="cg-button is-primary"
                disabled={savingPassword || !currentPassword || !newPassword}
              >
                {savingPassword ? '修改中…' : '修改密码'}
              </button>
            </div>
          </form>
        </SettingsCard>

        <section className="cg-semantic-card is-info">
          <header>
            <span className="cg-set-semantic-title">
              <RocketIcon size={15} />
              面板更新
            </span>
            <span className="cg-micro cg-set-semantic-tag">SYSTEM / UPDATE</span>
          </header>
          <div className="cg-semantic-body cg-set-semantic-body">
            <p className="cg-set-note">
              以 GitHub release 最新版本为标准检测更新；更新过程中面板操作将被锁定，
              下载/校验/解压/替换进度实时可见，完成后自动重启生效。
            </p>
            <div className="cg-set-facts">
              <span className="cg-set-note">当前版本</span>
              <span className="cg-pill">{settings.panel_version}</span>
              {versionInfo && versionInfo.latest && (
                <>
                  <span className="cg-set-note">最新版本</span>
                  <span className={cn('cg-pill', versionInfo.update_available && 'is-active')}>
                    {versionInfo.latest}
                  </span>
                </>
              )}
            </div>
            <div className="cg-set-actions">
              <Button
                variant="outline"
                disabled={checkingUpdate || startingUpdate}
                onClick={onCheckUpdate}
              >
                {checkingUpdate ? '检查中…' : '检查更新'}
              </Button>
              {versionInfo?.update_available && (
                <button
                  type="button"
                  className="cg-button is-primary"
                  disabled={startingUpdate}
                  onClick={onStartUpdate}
                >
                  {startingUpdate ? '启动中…' : `更新到 ${versionInfo.latest}`}
                </button>
              )}
              {versionInfo && !versionInfo.update_available && versionInfo.can_update && (
                <Button variant="destructive" disabled={startingUpdate} onClick={onForceUpdate}>
                  {startingUpdate ? '启动中…' : '强制更新'}
                </Button>
              )}
            </div>
            {updateError && <p className="cg-set-msg-err">{updateError}</p>}
            {versionInfo && !versionInfo.update_available && !updateError && (
              <p className="cg-set-note">{versionInfo.message || '已是最新版本'}</p>
            )}
          </div>
        </section>

        <section className="cg-semantic-card is-bad">
          <header>
            <span className="cg-set-semantic-title">
              <DatabaseBackupIcon size={15} />
              面板维护
            </span>
            <span className="cg-micro cg-set-semantic-tag">SYSTEM / MAINTENANCE</span>
          </header>
          <div className="cg-semantic-body cg-set-semantic-body">
            <p className="cg-set-note">
              重启面板进程：TLS 等重启生效项、以及后续面板版本更新都经此生效。 Docker 模式由容器
              restart policy 拉起，原生安装由 systemd 拉起； 仅非托管运行时由面板自派生新进程接管。
            </p>
            <div className="cg-set-actions">
              <Button variant="destructive" disabled={restarting} onClick={onRestart}>
                {restarting ? '重启中，请稍候…' : '重启面板'}
              </Button>
              <Button
                variant="outline"
                onClick={() =>
                  void api.downloadBackup().catch((err) => setError(errorMessage(err)))
                }
              >
                下载备份
              </Button>
            </div>
            <p className="cg-set-note">
              备份为业务 SQLite 数据库快照（VACUUM INTO），可直接替换数据文件恢复；
              <strong>不包含操作日志和请求日志</strong>。
            </p>
          </div>
        </section>
      </div>
    </Page>
  )
}
