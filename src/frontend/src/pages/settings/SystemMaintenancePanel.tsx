import { useState, type FormEvent } from 'react'
import { DatabaseBackupIcon, KeyRoundIcon, RocketIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api, errorMessage } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { cn } from '@/lib/utils'
import type { PanelSettings, PanelVersionInfo } from '@/lib/types'

import { SettingsCard } from './SettingsCard'
import type { PanelRestartController } from './use-panel-restart'

/**
 * 系统维护分区：修改密码、面板更新与面板维护。
 * 均为独立表单与操作，不参与主设置表单的统一保存。
 */
export function SystemMaintenancePanel({
  hidden,
  settings,
  restart,
  onError,
}: {
  hidden: boolean
  settings: PanelSettings
  restart: PanelRestartController
  onError: (message: string) => void
}) {
  const { confirm } = useAppDialog()
  // 密码
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [savingPassword, setSavingPassword] = useState(false)
  const [passwordMessage, setPasswordMessage] = useState('')
  const [passwordError, setPasswordError] = useState('')
  // 面板更新
  const [versionInfo, setVersionInfo] = useState<PanelVersionInfo | null>(null)
  const [checkingUpdate, setCheckingUpdate] = useState(false)
  const [startingUpdate, setStartingUpdate] = useState(false)
  const [updateError, setUpdateError] = useState('')
  const { restarting, onRestart } = restart

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

  return (
    <div className="cg-set-panel" hidden={hidden}>
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
              onClick={() => void api.downloadBackup().catch((err) => onError(errorMessage(err)))}
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
  )
}
