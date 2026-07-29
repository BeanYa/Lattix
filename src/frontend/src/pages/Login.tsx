import { useState, type FormEvent } from 'react'
import { RouteIcon, ServerIcon, UsersIcon } from 'lucide-react'
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
    <div className="relative grid min-h-[100dvh] place-items-center bg-background p-4 md:p-8">
      <ThemeToggle className="absolute right-4 top-4 bg-card text-foreground hover:bg-accent hover:text-accent-foreground md:right-8 md:top-8" />
      <div className="grid w-full max-w-5xl overflow-hidden rounded-lg border-2 border-border bg-card shadow-[0_8px_0_var(--shadow-color)] lg:grid-cols-[1.1fr_0.9fr]">
        <section className="relative hidden min-h-[620px] overflow-hidden border-r-2 border-border bg-[var(--pastel-blue)] p-10 lg:flex lg:flex-col lg:justify-between">
          <div className="flex items-center gap-3">
            <LattixMark className="size-12 shrink-0" />
            <div>
              <strong className="block text-lg font-normal tracking-normal" aria-label="Lattix">LATTIX</strong>
              <span className="text-xs text-muted-foreground">网络控制中心</span>
            </div>
          </div>
          <div className="relative z-10">
            <h1 className="max-w-md text-4xl font-extrabold leading-tight">让节点、链路和用户清晰可见。</h1>
            <p className="mt-5 max-w-md text-base leading-7 text-muted-foreground">从一个友好、可靠的控制面板管理整个网络。</p>
          </div>
          <div className="relative h-48" aria-hidden="true">
            <div className="absolute inset-x-0 bottom-0 h-24 rounded-t-[50%] bg-[var(--pastel-green)]" />
            <div className="absolute bottom-12 left-[12%] grid size-20 place-items-center rounded-lg border-2 bg-[var(--brand-yellow)] shadow-[0_5px_0_var(--shadow-color)]">
              <ServerIcon className="size-9" />
            </div>
            <div className="absolute bottom-6 left-1/2 grid size-24 -translate-x-1/2 place-items-center rounded-lg border-2 bg-[var(--brand-coral)] shadow-[0_5px_0_var(--shadow-color)]">
              <RouteIcon className="size-10" />
            </div>
            <div className="absolute bottom-14 right-[10%] grid size-20 place-items-center rounded-lg border-2 bg-[var(--brand-lilac)] shadow-[0_5px_0_var(--shadow-color)]">
              <UsersIcon className="size-8" />
            </div>
          </div>
        </section>
        <Card className="w-full rounded-none border-0 py-0 shadow-none ring-0">
          <CardHeader className="border-b px-7 py-8 md:px-10">
            <CardTitle className="text-2xl font-extrabold">欢迎回来</CardTitle>
            <CardDescription className="mt-1">登录 Lattix 管理面板</CardDescription>
          </CardHeader>
          <CardContent className="px-7 py-8 md:px-10 md:py-10">
            <form onSubmit={onSubmit} className="space-y-4">
              <div className="space-y-2">
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
              <div className="space-y-2">
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
              {error && (
                <Notice tone="danger">{error}</Notice>
              )}
              <Button type="submit" size="lg" className="mt-2 w-full" disabled={submitting}>
                {submitting ? '登录中…' : '登录'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
