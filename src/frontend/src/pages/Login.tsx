import { useState, type FormEvent } from 'react'
import { LockKeyholeIcon, RouteIcon, ServerIcon, ShieldCheckIcon } from 'lucide-react'
import { useLocation } from 'wouter'

import LattixMark from '@/components/LattixMark'
import { Notice } from '@/components/PagePrimitives'
import ThemeToggle from '@/components/ThemeToggle'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'

import './login.css'

export default function Login() {
  const { login } = useAuth()
  const [, navigate] = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setSubmitting(true)
    try {
      await login(username, password)
      navigate('/', { replace: true })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login">
      <header className="login-bar">
        <span className="login-brand">
          <span className="login-brand-mark">
            <LattixMark className="size-4 shrink-0" />
          </span>
          <span className="login-brand-name">LATTIX</span>
          <span className="login-brand-sub">NETWORK CONTROL PANEL</span>
        </span>
        <span className="login-bar-meta">自托管代理编排 / SELF-HOSTED</span>
        <span className="login-bar-spacer" />
        <ThemeToggle />
      </header>

      <main className="login-main">
        <section className="login-card" aria-label="Lattix 登录">
          <aside className="login-brief" aria-label="控制台说明">
            <h2 className="login-brief-title">
              一条长连接
              <br />
              管理整支机群
            </h2>
            <ul className="login-facts">
              <li>
                <RouteIcon size={13} strokeWidth={1.8} aria-hidden="true" />
                <span>
                  <strong>逐跳链路</strong>
                  入口、中转与出口服务器的有序拓扑
                </span>
              </li>
              <li>
                <ServerIcon size={13} strokeWidth={1.8} aria-hidden="true" />
                <span>
                  <strong>Agent 主动外连</strong>
                  面板无需开放反向端口
                </span>
              </li>
              <li>
                <ShieldCheckIcon size={13} strokeWidth={1.8} aria-hidden="true" />
                <span>
                  <strong>单管理员控制台</strong>
                  节点与用户热操作、失败回滚
                </span>
              </li>
            </ul>
            <p className="login-brief-foot">LATTIX — 多服务器 xray 代理管理面板</p>
          </aside>

          <section className="login-form" aria-labelledby="login-title">
            <div className="login-form-head">
              <h1 className="login-title" id="login-title">
                登录控制台
              </h1>
              <p className="login-sub">使用管理员凭据进入 Lattix 面板。</p>
            </div>
            <form onSubmit={onSubmit} className="login-fields">
              <div className="login-field">
                <Label htmlFor="username">用户名</Label>
                <Input
                  id="username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  autoComplete="username"
                  required
                  autoFocus
                />
              </div>
              <div className="login-field">
                <Label htmlFor="password">密码</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  required
                />
              </div>
              {error ? (
                <Notice tone="danger" title="登录失败">
                  {error}
                </Notice>
              ) : null}
              <button
                type="submit"
                className="cg-button is-primary login-submit"
                disabled={submitting}
              >
                <LockKeyholeIcon size={13} strokeWidth={2} aria-hidden="true" />
                {submitting ? '正在验证…' : '进入控制台'}
              </button>
            </form>
            <p className="login-hint">
              <ShieldCheckIcon size={11} strokeWidth={2} aria-hidden="true" />
              会话保存在本机浏览器；管理员密码由部署时设置。
            </p>
          </section>
        </section>
      </main>

      <footer className="login-foot">
        <span>LATTIX / NETWORK CONTROL PANEL</span>
        <span className="login-foot-end">SECURE SESSION</span>
      </footer>
    </div>
  )
}
