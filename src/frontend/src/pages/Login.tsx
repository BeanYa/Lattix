import { useState, type FormEvent } from 'react'
import { LockKeyholeIcon, ServerIcon, ShieldCheckIcon } from 'lucide-react'
import { useLocation } from 'wouter'

import LattixMark from '@/components/LattixMark'
import { Notice } from '@/components/PagePrimitives'
import ThemeToggle from '@/components/ThemeToggle'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/api'
import { useAuth } from '@/lib/auth'

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
    <div className="panel-canvas relative grid min-h-[100dvh] place-items-center p-4 md:p-8">
      <ThemeToggle className="absolute right-4 top-4 border bg-card/80 text-foreground hover:bg-accent hover:text-accent-foreground md:right-8 md:top-8" />
      <div className="grid w-full max-w-[820px] overflow-hidden rounded-md border border-border bg-card/90 shadow-[inset_0_1px_0_var(--panel-highlight),0_28px_90px_var(--shadow-color)] backdrop-blur-xl lg:grid-cols-[320px_minmax(0,1fr)]">
        <section className="relative hidden min-h-[500px] flex-col border-r border-sidebar-border bg-sidebar p-7 text-sidebar-foreground lg:flex">
          <div className="flex items-center gap-3">
            <LattixMark className="size-10 shrink-0" />
            <div>
              <strong className="block text-sm font-semibold" aria-label="Lattix">LATTIX</strong>
              <span className="text-[10px] text-sidebar-foreground/42">NETWORK CONTROL</span>
            </div>
          </div>

          <div className="my-auto">
            <span className="grid size-11 place-items-center rounded-sm border border-sidebar-border bg-sidebar-accent text-sidebar-primary shadow-[inset_0_1px_0_rgb(255_255_255_/_0.04)]">
              <LockKeyholeIcon className="size-5" strokeWidth={1.7} />
            </span>
            <h1 className="mt-5 text-2xl font-semibold leading-tight">管理面板登录</h1>
            <p className="mt-3 text-sm leading-6 text-sidebar-foreground/52">使用管理员凭证进入 Lattix 控制中心。</p>
          </div>

          <div className="grid gap-2 border-t border-sidebar-border pt-4 text-[11px] text-sidebar-foreground/55">
            <span className="flex items-center gap-2">
              <span className="size-1.5 rounded-full bg-sidebar-primary shadow-[0_0_9px_var(--sidebar-primary)]" />
              控制服务可用
            </span>
            <span className="flex items-center gap-2"><ShieldCheckIcon className="size-3.5" />安全会话</span>
            <span className="flex items-center gap-2"><ServerIcon className="size-3.5" />Lattix Panel</span>
          </div>
        </section>
        <Card className="w-full rounded-none border-0 bg-transparent py-0 shadow-none">
          <CardHeader className="border-b px-7 py-7 md:px-10">
            <div className="mb-3 flex items-center gap-3 lg:hidden">
              <LattixMark className="size-9" />
              <strong className="text-sm font-semibold">LATTIX</strong>
            </div>
            <CardTitle className="text-xl font-semibold">欢迎回来</CardTitle>
            <CardDescription className="mt-1 text-xs">登录 Lattix 管理面板</CardDescription>
          </CardHeader>
          <CardContent className="px-7 py-8 md:px-10 md:py-9">
            <form onSubmit={onSubmit} className="space-y-5">
              <div className="space-y-2">
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
              <div className="space-y-2">
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
              <Button type="submit" size="lg" className="mt-2 h-10 w-full" disabled={submitting}>
                {submitting ? '登录中…' : '登录'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
