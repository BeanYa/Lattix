import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertTriangleIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-react'

import { CopyButton } from '@/components/CopyButton'
import { Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { ServerMonitorGrid } from '@/components/ServerMonitor'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { formatDateTime } from '@/lib/format'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { isServerOnline } from '@/lib/server-state'
import { useTimezone } from '@/lib/timezone'
import { usePolling } from '@/lib/use-polling'

import './servers.css'
import { ServerCreateDialog } from './servers/ServerCreateDialog'
import { ServerEditDialog } from './servers/ServerEditDialog'
import { addInterval, localDate } from './servers/server-form-utils'
import { useServerCreateForm } from './servers/use-server-create-form'
import { useServerEditForm } from './servers/use-server-edit-form'
import type {
  CleanupXrayResult,
  Provider,
  ReleaseVersions,
  RebuildXrayResult,
  Server,
  ServerMetricSeries,
} from '@/lib/types'

const DEPENDENCIES_COMMAND = 'apk add --no-cache bash curl ca-certificates unzip util-linux'

export default function Servers() {
  const { timezone } = useTimezone()
  const { confirm, notify } = useAppDialog()
  const { showOperation } = useOperationProgress()
  const [servers, setServers] = useState<Server[]>([])
  const [metricSamples, setMetricSamples] = useState<ServerMetricSeries[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [providers, setProviders] = useState<Provider[]>([])
  const [providerManagerOpen, setProviderManagerOpen] = useState(false)
  const [providerEditID, setProviderEditID] = useState<number | null>(null)
  const [providerName, setProviderName] = useState('')
  const [providerWebsite, setProviderWebsite] = useState('')
  const [providerError, setProviderError] = useState('')
  const [cmdView, setCmdView] = useState<{
    title: string
    command: string
    insecure: boolean
  } | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Server | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [renewTarget, setRenewTarget] = useState<Server | null>(null)
  const [renewalOn, setRenewalOn] = useState('')
  const [renewing, setRenewing] = useState(false)
  const [upgradeTarget, setUpgradeTarget] = useState<Server | null>(null)
  const [upgradeKind, setUpgradeKind] = useState<'xray' | 'agent'>('xray')
  const [upgradeVersion, setUpgradeVersion] = useState('latest')
  const [upgradeVersions, setUpgradeVersions] = useState<ReleaseVersions | null>(null)
  const [upgradeVersionsLoading, setUpgradeVersionsLoading] = useState(false)
  const upgradeVersionsRequest = useRef(0)
  const [upgrading, setUpgrading] = useState(false)
  const [upgradeError, setUpgradeError] = useState('')
  // 升级命令追踪：下发后轮询命令终态（acked/failed），替代旧版"alert 后即关闭"。
  const [upgradeCmdId, setUpgradeCmdId] = useState<number | null>(null)
  const [upgradeResult, setUpgradeResult] = useState<'pending' | 'success' | 'failed' | null>(null)
  const [upgradeResultError, setUpgradeResultError] = useState('')
  // 清理 xray 缓存（xray.cleanup）：两步（dry-run 预览 → 确认执行）。
  const [cleanupTarget, setCleanupTarget] = useState<Server | null>(null)
  const [cleanupPreview, setCleanupPreview] = useState<CleanupXrayResult | null>(null)
  const [cleanupDone, setCleanupDone] = useState(false)
  const [cleanupBusy, setCleanupBusy] = useState(false)
  const [cleanupError, setCleanupError] = useState('')
  // 归一化回执数组：旧 agent 可能返回 null（Go nil 切片），渲染直接读 .length 会崩溃。
  const normalizeCleanup = (r: CleanupXrayResult): CleanupXrayResult => ({
    removed_inbounds: r.removed_inbounds ?? [],
    removed_pieces: r.removed_pieces ?? [],
  })
  // 重建 xray 配置（xray.rebuild）：单步确认执行，回执展示重建结果或回滚提示。
  const [rebuildTarget, setRebuildTarget] = useState<Server | null>(null)
  const [rebuildResult, setRebuildResult] = useState<RebuildXrayResult | null>(null)
  const [rebuildDone, setRebuildDone] = useState(false)
  const [rebuildBusy, setRebuildBusy] = useState(false)
  const [rebuildError, setRebuildError] = useState('')
  const normalizeRebuild = (r: RebuildXrayResult): RebuildXrayResult => ({
    rebuilt_inbounds: r.rebuilt_inbounds ?? [],
    rebuilt_pieces: r.rebuilt_pieces ?? [],
    rolled_back: r.rolled_back ?? false,
  })

  const onRebuildXray = (s: Server) => {
    setRebuildTarget(s)
    setRebuildResult(null)
    setRebuildDone(false)
    setRebuildError('')
  }

  const runRebuildXray = async () => {
    if (!rebuildTarget) {
      return
    }
    setRebuildBusy(true)
    setRebuildError('')
    try {
      const { data: result, observeId } = await api.rebuildXray(rebuildTarget.id)
      if (observeId) showOperation({ observeId })
      setRebuildResult(normalizeRebuild(result))
      setRebuildDone(true)
      load()
    } catch (err) {
      setRebuildError(errorMessage(err))
    } finally {
      setRebuildBusy(false)
    }
  }
  const serverListRequest = useRef(0)

  const load = useCallback(async (silent = false, signal?: AbortSignal) => {
    const request = ++serverListRequest.current
    const options = signal
      ? { signal, ...(silent ? { display: 'silent' as const } : {}) }
      : silent
        ? { display: 'silent' as const }
        : undefined
    try {
      const nextServers = await api.servers(options)
      if (signal?.aborted || request !== serverListRequest.current) return
      setServers(nextServers)
    } catch (err) {
      if (signal?.aborted || request !== serverListRequest.current) return
      setError(errorMessage(err))
    } finally {
      if (!signal?.aborted && request === serverListRequest.current) setLoading(false)
    }
  }, [])

  const loadProviders = useCallback(
    () =>
      api
        .providers()
        .then(setProviders)
        .catch(() => setProviders([])),
    [],
  )

  usePolling(load, serverListRequest)

  useEffect(() => {
    loadProviders()
  }, [loadProviders])

  useEffect(() => {
    let active = true
    const loadSamples = () => {
      api
        .serverMetricSamples()
        .then((result) => {
          if (active) setMetricSamples(result)
        })
        .catch(() => {
          if (active) setMetricSamples([])
        })
    }
    loadSamples()
    const timer = setInterval(loadSamples, 60000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  const createForm = useServerCreateForm({
    onCreated: (res) =>
      setCmdView({
        title: '服务器已创建，请在目标机器上执行安装命令',
        command: res.install_command,
        insecure: res.install_insecure,
      }),
    reload: load,
  })
  const editForm = useServerEditForm({ reload: load })

  // 未安装（从未上线）→ 重新获取安装命令；已安装 → 凭证刷新（旧凭证立即失效）。
  const onRotateToken = async (s: Server) => {
    const installed = s.last_seen_at !== null
    if (
      installed &&
      !(await confirm({
        title: '刷新服务器凭证',
        description:
          '刷新后该服务器的旧凭证（含长期凭证）立即失效，agent 重连前需重新执行安装命令。',
        confirmLabel: '刷新凭证',
        destructive: true,
      }))
    ) {
      return
    }
    try {
      const res = await api.rotateServerToken(s.id)
      setCmdView({
        title: installed ? '凭证已刷新，请重新执行安装命令' : '安装命令（bootstrap token 已刷新）',
        command: res.install_command,
        insecure: res.install_insecure,
      })
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  // 配置漂移修复（§17）：重放该服务器全部 active 节点，agent 重建配置后漂移标志自动清除。
  const onRepair = async (s: Server) => {
    if (
      !(await confirm({
        title: '修复配置漂移',
        description: `确定修复「${s.alias}」的配置漂移？将按面板节点状态重建该机 xray 配置。`,
        confirmLabel: '开始修复',
      }))
    ) {
      return
    }
    try {
      const { data: res, observeId } = await api.repairServer(s.id)
      if (observeId) showOperation({ observeId })
      setError('')
      await notify({
        title: '修复命令已下发',
        description: `已下发 ${res.reapplied} 个节点的重放命令，漂移标志将在 agent 重建后自动清除。`,
      })
      load()
    } catch (err) {
      setError(errorMessage(err))
    }
  }

  // 清理 xray 缓存（xray.cleanup）：先 dry-run 预览差异，确认后执行。
  const onCleanupXray = async (s: Server) => {
    setCleanupTarget(s)
    setCleanupPreview(null)
    setCleanupDone(false)
    setCleanupError('')
    setCleanupBusy(true)
    try {
      const { data: preview, observeId } = await api.cleanupXray(s.id, true)
      if (observeId) showOperation({ observeId })
      setCleanupPreview(normalizeCleanup(preview))
    } catch (err) {
      setCleanupError(errorMessage(err))
    } finally {
      setCleanupBusy(false)
    }
  }

  const runCleanupXray = async () => {
    if (!cleanupTarget) {
      return
    }
    setCleanupBusy(true)
    setCleanupError('')
    try {
      const { data: preview, observeId } = await api.cleanupXray(cleanupTarget.id, false)
      if (observeId) showOperation({ observeId })
      setCleanupPreview(normalizeCleanup(preview))
      setCleanupDone(true)
      load()
    } catch (err) {
      setCleanupError(errorMessage(err))
    } finally {
      setCleanupBusy(false)
    }
  }

  // 版本升级（§18）：命令入队后由 agent 下载/校验/替换/重启，版本号经 hello/遥测自动刷新。
  // kind=xray 升级 xray-core；kind=agent 升级 agent 自身（兼容窗口外的机器经此收敛）。
  const onOpenUpgrade = (s: Server, kind: 'xray' | 'agent') => {
    const requestID = ++upgradeVersionsRequest.current
    setUpgradeTarget(s)
    setUpgradeKind(kind)
    setUpgradeVersion(kind === 'xray' ? (s.effective_xray_version ?? 'latest') : 'latest')
    setUpgradeVersions(null)
    setUpgradeVersionsLoading(true)
    setUpgradeError('')
    setUpgradeCmdId(null)
    setUpgradeResult(null)
    setUpgradeResultError('')
    api
      .releaseVersions(kind)
      .then((versions) => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeVersions(versions)
      })
      .catch((err) => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeError(errorMessage(err))
      })
      .finally(() => {
        if (upgradeVersionsRequest.current === requestID) setUpgradeVersionsLoading(false)
      })
  }

  const onUpgrade = async (e: FormEvent) => {
    e.preventDefault()
    if (!upgradeTarget) {
      return
    }
    setUpgradeError('')
    setUpgrading(true)
    setUpgradeResult(null)
    setUpgradeResultError('')
    try {
      const version = upgradeVersion.trim() || 'latest'
      const res =
        upgradeKind === 'agent'
          ? await api.upgradeAgent(upgradeTarget.id, version)
          : await api.upgradeServer(upgradeTarget.id, version)
      // 下发成功：进入轮询模式（弹窗保留，显示命令执行进度直到终态）。
      setUpgradeCmdId(res.command_id)
      setUpgradeResult('pending')
    } catch (err) {
      setUpgradeError(errorMessage(err))
    } finally {
      setUpgrading(false)
    }
  }

  // 升级命令轮询：下发后跟踪 command_id 终态（acked=成功 / failed=失败），
  // 替代旧版"alert 后即关闭、失败无感知"。agent 升级成功后会退出重连，
  // 命令可能停在 sent（agent 重启未回执），故设超时兜底提示。
  useEffect(() => {
    if (upgradeResult !== 'pending' || upgradeCmdId === null || !upgradeTarget) {
      return
    }
    const serverId = upgradeTarget.id
    const cmdId = upgradeCmdId
    let stopped = false
    const poll = async () => {
      try {
        const cmds = await api.serverCommands(serverId, 50, { display: 'silent' })
        if (stopped) {
          return
        }
        const cmd = cmds.find((c) => c.id === cmdId)
        if (!cmd) {
          return // 命令尚未出现在日志，等下次轮询
        }
        if (cmd.status === 'acked') {
          setUpgradeResult('success')
          load() // 刷新服务器列表（版本号/在线状态）
        } else if (cmd.status === 'failed') {
          setUpgradeResult('failed')
          setUpgradeResultError(cmd.error || '命令执行失败（详见命令日志）')
        }
        // queued/sent：继续轮询
      } catch {
        // 轮询本身的网络错误静默，下次重试
      }
    }
    poll()
    const interval = setInterval(poll, 3000)
    // 90s 超时：agent 自升级成功会退出重连，可能来不及回执；提示用户自行核对版本。
    const timeout = setTimeout(() => {
      if (!stopped) {
        setUpgradeResult(null)
        setUpgradeResultError(
          '未在超时内收到 agent 回执（agent 自升级会重启重连，请稍后核对版本或查看命令日志）',
        )
      }
    }, 90000)
    return () => {
      stopped = true
      clearInterval(interval)
      clearTimeout(timeout)
    }
  }, [load, upgradeResult, upgradeCmdId, upgradeTarget])

  const onDelete = async (purge: 'xray' | 'agent') => {
    if (!deleteTarget) {
      return
    }
    setDeleting(true)
    try {
      await api.deleteServer(deleteTarget.id, purge)
      setDeleteTarget(null)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setDeleting(false)
    }
  }

  const saveProvider = async (event: FormEvent) => {
    event.preventDefault()
    setProviderError('')
    try {
      if (providerEditID) await api.updateProvider(providerEditID, providerName, providerWebsite)
      else await api.createProvider(providerName, providerWebsite)
      setProviderEditID(null)
      setProviderName('')
      setProviderWebsite('')
      await loadProviders()
    } catch (err) {
      setProviderError(errorMessage(err))
    }
  }

  const removeProvider = async (provider: Provider) => {
    if (
      !(await confirm({
        title: '删除服务商',
        description: `确定删除「${provider.name}」？已被服务器使用时无法删除。`,
        confirmLabel: '删除',
        destructive: true,
      }))
    )
      return
    try {
      await api.deleteProvider(provider.id)
      await loadProviders()
    } catch (err) {
      setProviderError(errorMessage(err))
    }
  }

  const openRenewal = (server: Server) => {
    setRenewTarget(server)
    setRenewalOn(
      addInterval(localDate(), server.billing.interval_count, server.billing.interval_unit),
    )
  }

  const confirmRenewal = async (event: FormEvent) => {
    event.preventDefault()
    if (!renewTarget) return
    setRenewing(true)
    try {
      await api.confirmServerRenewal(renewTarget.id, renewalOn)
      setRenewTarget(null)
      load()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRenewing(false)
    }
  }

  return (
    <Page className="cg-page-in">
      <div className="sv-topline">
        <span className="cg-eyebrow">SERVERS / FLEET</span>
        <span className="cg-pill">
          {loading ? '正在同步' : `${String(servers.length).padStart(2, '0')} NODES`}
        </span>
      </div>
      <PageHeader
        title="服务器"
        description="接入机器的在线状态、资源指标与生命周期管理。"
        actions={
          <button type="button" className="cg-button is-primary" onClick={createForm.openCreate}>
            <PlusIcon />
            添加服务器
          </button>
        }
      />

      {error && (
        <Notice tone="danger" title="加载失败">
          {error}
        </Notice>
      )}

      <ServerMonitorGrid
        servers={servers}
        samples={metricSamples}
        loading={loading}
        timezone={timezone}
        onEdit={editForm.openEdit}
        onRepair={onRepair}
        onCleanupXray={onCleanupXray}
        onRebuildXray={onRebuildXray}
        onRotateToken={onRotateToken}
        onUpgrade={onOpenUpgrade}
        onRenew={openRenewal}
        onDelete={setDeleteTarget}
      />

      <ServerCreateDialog
        controller={createForm}
        providers={providers}
        onManageProviders={() => setProviderManagerOpen(true)}
      />

      <Dialog open={cmdView !== null} onOpenChange={(next) => !next && setCmdView(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>安装命令</DialogTitle>
            <DialogDescription>{cmdView?.title}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <div className="flex items-center justify-between gap-3">
              <p className="text-sm text-muted-foreground">bash/curl 等依赖安装（按需执行）</p>
              <CopyButton text={DEPENDENCIES_COMMAND} />
            </div>
            <pre className="sv-code-block">{DEPENDENCIES_COMMAND}</pre>
          </div>
          <div className="space-y-2">
            <p className="text-sm font-medium">Agent 安装命令</p>
            <pre className="sv-code-block max-h-40">{cmdView?.command}</pre>
            {cmdView?.insecure ? (
              <div className="flex items-start gap-2 bg-warning/10 px-3 py-2 text-xs text-warning">
                <AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" />
                <span>
                  面板地址为明文 http：Agent 与面板间的控制流量可被窃听或篡改。跨公网部署请改用
                  https 反向代理。
                </span>
              </div>
            ) : null}
          </div>
          <DialogFooter showCloseButton>
            <CopyButton text={cmdView?.command ?? ''} size="default" />
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ServerEditDialog
        controller={editForm}
        providers={providers}
        onManageProviders={() => setProviderManagerOpen(true)}
      />

      <Dialog
        open={upgradeTarget !== null}
        onOpenChange={(next) => {
          if (!next) {
            upgradeVersionsRequest.current++
            setUpgradeTarget(null)
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>升级 {upgradeKind === 'agent' ? 'agent' : 'xray'}</DialogTitle>
            <DialogDescription>
              {upgradeKind === 'agent' ? (
                <>
                  将「{upgradeTarget?.alias}」的 agent 升级到指定版本（当前：
                  {upgradeTarget?.agent_version ?? '未知'}）。agent 将从 GitHub release
                  下载二进制、校验 SHA256 后自替换并重启；该操作也用于收敛落后出兼容窗口的 agent。
                </>
              ) : (
                <>
                  将「{upgradeTarget?.alias}」的 xray 升级到指定版本（当前：
                  {upgradeTarget?.xray_version ?? '未知'}）。agent 将下载官方 release、 校验
                  SHA2-256 后替换并重启；失败自动回滚。
                </>
              )}
            </DialogDescription>
            {upgradeKind === 'xray' && upgradeTarget?.effective_xray_version && (
              <span className="text-xs text-muted-foreground">
                期望版本：{upgradeTarget.effective_xray_version}
                {upgradeTarget.custom_settings ? '（服务器覆盖）' : '（面板默认）'}
              </span>
            )}
          </DialogHeader>
          <form onSubmit={onUpgrade} className="space-y-4">
            {upgradeResult === null && (
              <>
                <div className="space-y-2">
                  <Label htmlFor="upgradeVersion">目标版本</Label>
                  <Select
                    value={upgradeVersion}
                    onValueChange={(value) => value && setUpgradeVersion(value)}
                    items={(upgradeVersions?.versions ?? ['latest']).map((version) => ({
                      value: version,
                      label: version,
                    }))}
                    disabled={upgradeVersionsLoading}
                  >
                    <SelectTrigger id="upgradeVersion" className="w-full" autoFocus>
                      <SelectValue
                        placeholder={upgradeVersionsLoading ? '正在获取版本…' : '选择版本'}
                      />
                    </SelectTrigger>
                    <SelectContent>
                      {(upgradeVersions?.versions ?? ['latest']).map((version) => (
                        <SelectItem key={version} value={version}>
                          {version}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  {upgradeVersions && (
                    <p className="text-xs text-muted-foreground">
                      缓存更新于 {formatDateTime(upgradeVersions.fetched_at, timezone)}
                      {upgradeVersions.stale
                        ? `；${upgradeVersions.message ?? '本次更新失败，正在使用缓存'}`
                        : ''}
                    </p>
                  )}
                </div>
                {upgradeError && <p className="text-sm text-destructive">{upgradeError}</p>}
                <DialogFooter>
                  <Button type="submit" disabled={upgrading || upgradeVersionsLoading}>
                    {upgrading ? '下发中…' : '下发升级'}
                  </Button>
                </DialogFooter>
              </>
            )}
            {upgradeResult === 'pending' && (
              <div className="space-y-3">
                <p className="text-sm">
                  升级命令已下发（#{upgradeCmdId}），正在等待 agent 执行回执…
                </p>
                <p className="text-sm text-muted-foreground">
                  agent 自升级成功后会退出并由 systemd 拉起重连，可能需要数十秒。
                </p>
              </div>
            )}
            {upgradeResult === 'success' && (
              <div className="space-y-3">
                <p className="text-sm text-success">升级命令执行成功，版本号已刷新。</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
            {upgradeResult === 'failed' && (
              <div className="space-y-3">
                <p className="text-sm text-destructive">升级失败：</p>
                <p className="text-sm text-destructive whitespace-pre-wrap">{upgradeResultError}</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
            {/* 超时兜底（upgradeResult 被清空但 upgradeResultError 非空） */}
            {upgradeResult === null && upgradeResultError && (
              <div className="space-y-3">
                <p className="text-sm text-warning whitespace-pre-wrap">{upgradeResultError}</p>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setUpgradeTarget(null)}>
                    关闭
                  </Button>
                </DialogFooter>
              </div>
            )}
          </form>
        </DialogContent>
      </Dialog>

      <Dialog
        open={cleanupTarget !== null}
        onOpenChange={(next) => !next && setCleanupTarget(null)}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>清理 Xray 缓存</DialogTitle>
            <DialogDescription>
              {cleanupDone
                ? `「${cleanupTarget?.alias}」已清理完成，将 xray 配置中未被面板管理的残留配置件删除。`
                : `对比面板当前链路状态，删除「${cleanupTarget?.alias}」xray 配置中未被面板有效管理的监听与链路配置件。`}
            </DialogDescription>
          </DialogHeader>
          {cleanupError ? (
            <p className="text-sm text-destructive whitespace-pre-wrap">{cleanupError}</p>
          ) : null}
          {cleanupBusy ? (
            <p className="text-sm text-muted-foreground">
              {cleanupDone ? '已清理完成。' : '正在向 agent 下发检查…'}
            </p>
          ) : cleanupPreview ? (
            cleanupPreview.removed_inbounds.length === 0 &&
            cleanupPreview.removed_pieces.length === 0 ? (
              <p className="text-sm text-muted-foreground">无残留配置，xray 配置与面板状态一致。</p>
            ) : (
              <div className="space-y-3">
                {cleanupPreview.removed_inbounds.length > 0 ? (
                  <div className="space-y-1">
                    <p className="text-xs font-medium text-muted-foreground">
                      将删除 {cleanupPreview.removed_inbounds.length} 个监听（inbound）
                    </p>
                    <ul className="sv-mono-list max-h-48 space-y-1 overflow-y-auto text-sm">
                      {cleanupPreview.removed_inbounds.map((inbound) => (
                        <li
                          key={inbound.tag}
                          className="flex items-center justify-between gap-3 font-mono text-xs"
                        >
                          <span className="truncate">{inbound.tag}</span>
                          <span className="shrink-0 text-muted-foreground">
                            :{inbound.port || '?'}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
                {cleanupPreview.removed_pieces.length > 0 ? (
                  <div className="space-y-1">
                    <p className="text-xs font-medium text-muted-foreground">
                      将删除 {cleanupPreview.removed_pieces.length} 个链路配置件（piece）
                    </p>
                    <ul className="sv-mono-list max-h-32 space-y-1 overflow-y-auto font-mono text-xs">
                      {cleanupPreview.removed_pieces.map((piece) => (
                        <li key={piece}>{piece}</li>
                      ))}
                    </ul>
                  </div>
                ) : null}
              </div>
            )
          ) : null}
          <DialogFooter>
            {!cleanupDone ? (
              <>
                <Button
                  variant="outline"
                  disabled={cleanupBusy}
                  onClick={() => setCleanupTarget(null)}
                >
                  关闭
                </Button>
                <Button
                  disabled={
                    cleanupBusy ||
                    cleanupError !== '' ||
                    cleanupPreview === null ||
                    (cleanupPreview.removed_inbounds.length === 0 &&
                      cleanupPreview.removed_pieces.length === 0)
                  }
                  onClick={runCleanupXray}
                >
                  {cleanupBusy ? '执行中…' : '确认清理'}
                </Button>
              </>
            ) : (
              <Button onClick={() => setCleanupTarget(null)}>完成</Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={rebuildTarget !== null}
        onOpenChange={(next) => !next && setRebuildTarget(null)}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>重建 Xray 配置</DialogTitle>
            <DialogDescription>
              {rebuildDone
                ? rebuildResult?.rolled_back
                  ? `「${rebuildTarget?.alias}」重建失败，已恢复重建前的 xray.json。`
                  : `「${rebuildTarget?.alias}」已按当前生效的链路与用户配置重建 xray.json。`
                : `将停止「${rebuildTarget?.alias}」的 xray 服务，备份并重新生成 xray.json（保留现有私钥与端口），校验后重启并自检；失败会自动恢复备份。重建期间该服务器代理不可用。`}
            </DialogDescription>
          </DialogHeader>
          {rebuildError ? (
            <p className="text-sm text-destructive whitespace-pre-wrap">{rebuildError}</p>
          ) : null}
          {rebuildBusy ? (
            <p className="text-sm text-muted-foreground">正在向 agent 下发重建…</p>
          ) : rebuildResult ? (
            <div className="space-y-3">
              {rebuildResult.rolled_back ? (
                <p className="text-sm text-destructive">
                  重建失败，已恢复重建前的 xray.json 并重启 xray。
                </p>
              ) : (
                <>
                  <p className="text-sm text-muted-foreground">
                    已重建 {rebuildResult.rebuilt_inbounds.length} 个监听与{' '}
                    {rebuildResult.rebuilt_pieces.length} 个链路配置件。
                  </p>
                  {rebuildResult.rebuilt_inbounds.length > 0 ? (
                    <ul className="sv-mono-list max-h-48 space-y-1 overflow-y-auto font-mono text-xs">
                      {rebuildResult.rebuilt_inbounds.map((inbound) => (
                        <li key={inbound.tag} className="flex items-center justify-between gap-3">
                          <span className="truncate">{inbound.tag}</span>
                          <span className="shrink-0 text-muted-foreground">
                            :{inbound.port || '?'}
                          </span>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </>
              )}
            </div>
          ) : null}
          <DialogFooter>
            {!rebuildDone ? (
              <>
                <Button
                  variant="outline"
                  disabled={rebuildBusy}
                  onClick={() => setRebuildTarget(null)}
                >
                  取消
                </Button>
                <Button variant="default" disabled={rebuildBusy} onClick={runRebuildXray}>
                  {rebuildBusy ? '重建中…' : '确认重建'}
                </Button>
              </>
            ) : (
              <Button onClick={() => setRebuildTarget(null)}>完成</Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteTarget !== null} onOpenChange={(next) => !next && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除服务器</DialogTitle>
            <DialogDescription>
              {deleteTarget && isServerOnline(deleteTarget)
                ? `确定删除「${deleteTarget.alias}」？将向 agent 发送卸载命令并删除记录，请选择卸载范围。`
                : `确定删除「${deleteTarget?.alias}」？当前无可投递会话，仅删除记录；该机上的 agent 需手动清理。`}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            {deleteTarget && isServerOnline(deleteTarget) ? (
              <>
                <Button variant="outline" disabled={deleting} onClick={() => onDelete('agent')}>
                  仅卸载 agent
                </Button>
                <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                  连同 xray 卸载
                </Button>
              </>
            ) : (
              <Button variant="destructive" disabled={deleting} onClick={() => onDelete('xray')}>
                删除记录
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={providerManagerOpen} onOpenChange={setProviderManagerOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>服务商管理</DialogTitle>
            <DialogDescription>
              服务器列表中仅显示服务商名称，官网用于快捷打开控制台。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={saveProvider} className="space-y-3">
            <div className="space-y-2">
              <Label>服务商名称</Label>
              <Input
                value={providerName}
                onChange={(e) => setProviderName(e.target.value)}
                required
                maxLength={100}
              />
            </div>
            <div className="space-y-2">
              <Label>官网地址</Label>
              <Input
                type="url"
                value={providerWebsite}
                onChange={(e) => setProviderWebsite(e.target.value)}
                placeholder="https://provider.example"
              />
            </div>
            {providerError ? <p className="text-sm text-destructive">{providerError}</p> : null}
            <DialogFooter>
              <Button type="submit">{providerEditID ? '保存修改' : '添加服务商'}</Button>
            </DialogFooter>
          </form>
          <Separator />
          <div className="max-h-56 space-y-2 overflow-y-auto">
            {providers.map((provider) => (
              <div
                key={provider.id}
                className="flex items-center justify-between gap-3 border-b py-2 last:border-0"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{provider.name}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {provider.website_url || '未配置官网'}
                  </p>
                </div>
                <div className="flex gap-1">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    title="编辑服务商"
                    onClick={() => {
                      setProviderEditID(provider.id)
                      setProviderName(provider.name)
                      setProviderWebsite(provider.website_url)
                    }}
                  >
                    <PencilIcon />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    title="删除服务商"
                    onClick={() => removeProvider(provider)}
                  >
                    <Trash2Icon />
                  </Button>
                </div>
              </div>
            ))}
            {providers.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-foreground">暂无服务商</p>
            ) : null}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={renewTarget !== null} onOpenChange={(next) => !next && setRenewTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>续费确认</DialogTitle>
            <DialogDescription>
              确认「{renewTarget?.alias}」已经续费，并设置新的下次续费日。
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={confirmRenewal} className="space-y-4">
            <div className="space-y-2">
              <Label>下次续费日</Label>
              <Input
                type="date"
                min={addInterval(localDate(), 1, 'day')}
                value={renewalOn}
                onChange={(e) => setRenewalOn(e.target.value)}
                required
              />
            </div>
            <DialogFooter>
              <Button type="submit" disabled={renewing}>
                {renewing ? '确认中…' : '确认续费'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Page>
  )
}
