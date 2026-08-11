import { useState, type FormEvent } from 'react'
import { LockKeyholeIcon, ServerIcon, ShieldCheckIcon } from 'lucide-react'
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
    <div className="cg-login">
      <ThemeToggle className="cg-login-theme" />
      <div className="cg-card-raised cg-login-card">
        {/* 品牌区（桌面端左侧 Charcoal 技术面板） */}
        <aside className="cg-login-brand">
          <div className="cg-login-brand-row">
            <span className="cg-login-brand-mark">
              <LattixMark className="size-6" />
            </span>
            <div>
              <strong className="cg-login-brand-name" aria-label="Lattix">LATTIX</strong>
              <span className="cg-login-brand-sub">NETWORK CONTROL</span>
            </div>
          </div>

          <div className="cg-login-brand-hero">
            <span className="cg-micro" style={{ color: 'var(--cg-lime)' }}>OPS MANUAL / VOL.01</span>
            <p className="cg-login-brand-tag">CHAIN ROUTING · SUBSCRIPTION OPS</p>
            <p className="cg-login-brand-desc">代理链路、订阅访问与运行审计的工程手册式控制台。</p>
          </div>

          <div className="cg-login-brand-foot">
            <span className="cg-login-status"><i />控制服务可用</span>
            <span className="cg-login-brand-line"><ShieldCheckIcon />安全会话 / SECURE SESSION</span>
            <span className="cg-login-brand-line"><ServerIcon />LATTIX PANEL</span>
          </div>
        </aside>

        {/* 登录卡 */}
        <section className="cg-login-form">
          <div className="cg-login-form-mobile">
            <LattixMark className="size-9" />
            <strong>LATTIX</strong>
          </div>
          <span className="cg-login-form-icon">
            <LockKeyholeIcon />
          </span>
          <div className="cg-login-form-head">
            <span className="cg-eyebrow">ACCESS / SIGN IN</span>
            <h1 className="cg-title cg-login-form-title">欢迎回来</h1>
            <p className="cg-login-form-sub">登录 Lattix 管理面板 / ADMIN CONSOLE</p>
          </div>
          <form onSubmit={onSubmit} className="cg-login-fields">
            <div className="cg-login-field">
              <Label htmlFor="username">用户名</Label>
              <Input
                id="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                required
                autoFocus
                className="h-10"
              />
            </div>
            <div className="cg-login-field">
              <Label htmlFor="password">密码</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
                className="h-10"
              />
            </div>
            {error && (
              <Notice tone="danger">{error}</Notice>
            )}
            <button type="submit" className="cg-button is-primary cg-login-submit" disabled={submitting}>
              {submitting ? '登录中…' : '登录'}
            </button>
          </form>
        </section>
      </div>
    </div>
  )
}
