# IPv6 不可用时隐藏相关章节 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 当服务器无全局 IPv6 地址时，服务器测试的报告/选择/进度三个弹窗不再渲染 IPv6 相关章节，仅提示「IPv6 不可用」。

**Architecture:** Agent 在 `inspectEnvironment` 中检测本机全局 IPv6 地址，写入报告 `environment.ipv6_available`；前端以 `=== false` 严格判断该信号，在三个弹窗中过滤 `tcp_ipv6`/`cernet2_ipv6`/`return_route_ipv6` 三个类别。选择/进度弹窗复用该服务器最近一次报告的信号，零新 API。

**Tech Stack:** Go 1.26（go.work 多模块）、React + TypeScript（Vite）、Vitest。

## Global Constraints

- `ipv6_available` 语义：字段缺失/`undefined` = 未知（不隐藏）；只有显式 `false` 才隐藏。
- 不新增面板 API / RPC 字段 / OpenAPI 变更；选择与进度弹窗的信号来自 `task.result.environment`。
- 信号含义是「本机是否存在可用全局 IPv6 地址」（排除 loopback / link-local / ULA / IPv4-mapped），不是测试目标可达性。
- 三个类别常量：`tcp_ipv6`、`cernet2_ipv6`、`return_route_ipv6`。
- Go 测试命令（仓库根，go.work 生效）：`go test ./src/agent/internal/servertest/...`
- 前端验证命令（`src/frontend` 目录）：`npm run lint`、`npx tsc -b`、`npm test`（vitest 全量）
- 提交信息风格：`feat(agent): ...` / `feat(panel): ...`

---

### Task 1: Agent 检测全局 IPv6 地址

**Files:**
- Modify: `src/shared/server_testing.go`（`ServerTestEnvironment` 增加字段）
- Create: `src/agent/internal/servertest/ipv6.go`
- Create: `src/agent/internal/servertest/ipv6_test.go`
- Modify: `src/agent/internal/servertest/runner.go:149-152`（`inspectEnvironment` 填充新字段）

**Interfaces:**
- Produces: `shared.ServerTestEnvironment.IPv6Available bool \`json:"ipv6_available"\``；`servertest.hasGlobalIPv6() bool`（包内私有，`inspectEnvironment` 调用）；`servertest.usableGlobalIPv6(ip netip.Addr) bool`（可测纯函数）。
- Consumes: 无（本任务第一个）。

- [ ] **Step 1: 编写失败测试**

创建 `src/agent/internal/servertest/ipv6_test.go`：

```go
package servertest

import (
	"errors"
	"net"
	"net/netip"
	"testing"
)

var errTestInterfaces = errors.New("interfaces unavailable")

func TestUsableGlobalIPv6(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"2400:3200::1", true},
		{"2606:4700::1111", true},
		{"2001:db8::1", true},
		{"::1", false},
		{"fe80::1", false},
		{"fc00::1", false},
		{"fd12:3456:789a::1", false},
		{"::ffff:192.0.2.1", false},
		{"192.168.1.1", false},
		{"", false},
	}
	for _, tc := range cases {
		ip, err := netip.ParseAddr(tc.ip)
		if err != nil {
			ip = netip.Addr{}
		}
		if got := usableGlobalIPv6(ip); got != tc.want {
			t.Errorf("usableGlobalIPv6(%q) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestHasGlobalIPv6(t *testing.T) {
	originalList, originalAddrs := listInterfaces, interfaceAddresses
	defer func() { listInterfaces, interfaceAddresses = originalList, originalAddrs }()

	t.Run("global address present", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}, {Index: 2, Name: "lo"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) {
			if iface.Name == "lo" {
				return []net.Addr{&net.IPAddr{IP: net.ParseIP("::1")}}, nil
			}
			return []net.Addr{&net.IPAddr{IP: net.ParseIP("2400:3200::1")}}, nil
		}
		if !hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = false, want true")
		}
	})

	t.Run("link-local and ula only", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) {
			return []net.Interface{{Index: 1, Name: "eth0"}}, nil
		}
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) {
			return []net.Addr{
				&net.IPAddr{IP: net.ParseIP("fe80::1")},
				&net.IPAddr{IP: net.ParseIP("fd12:3456::1")},
			}, nil
		}
		if hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = true, want false")
		}
	})

	t.Run("interface enumeration error", func(t *testing.T) {
		listInterfaces = func() ([]net.Interface, error) { return nil, errTestInterfaces }
		interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) { return nil, nil }
		if hasGlobalIPv6() {
			t.Error("hasGlobalIPv6() = true, want false")
		}
	})
}
```

- [ ] **Step 2: 运行测试确认失败（编译失败即可）**

Run: `go test ./src/agent/internal/servertest/...`
Expected: FAIL —— `undefined: usableGlobalIPv6`、`undefined: listInterfaces`、`undefined: hasGlobalIPv6`

- [ ] **Step 3: 实现 ipv6.go**

创建 `src/agent/internal/servertest/ipv6.go`：

```go
package servertest

import (
	"net"
	"net/netip"
)

var (
	listInterfaces     = func() ([]net.Interface, error) { return net.Interfaces() }
	interfaceAddresses = func(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() }
)

// hasGlobalIPv6 reports whether the machine has at least one usable global
// unicast IPv6 address (excluding loopback, link-local, ULA and IPv4-mapped).
func hasGlobalIPv6() bool {
	ifaces, err := listInterfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, err := interfaceAddresses(iface)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if usableGlobalIPv6(addrFromNetAddr(addr)) {
				return true
			}
		}
	}
	return false
}

func addrFromNetAddr(addr net.Addr) netip.Addr {
	switch value := addr.(type) {
	case *net.IPNet:
		ip, _ := netip.AddrFromSlice(value.IP)
		return ip
	case *net.IPAddr:
		ip, _ := netip.AddrFromSlice(value.IP)
		return ip
	}
	return netip.Addr{}
}

// usableGlobalIPv6 accepts global unicast IPv6 addresses that are neither
// loopback, link-local, ULA nor IPv4-mapped.
func usableGlobalIPv6(ip netip.Addr) bool {
	return ip.Is6() && !ip.Is4In6() && ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() && !ip.IsPrivate()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./src/agent/internal/servertest/...`
Expected: PASS（`TestUsableGlobalIPv6`、`TestHasGlobalIPv6` 全部通过）

- [ ] **Step 5: 共享类型增加字段**

`src/shared/server_testing.go` 的 `ServerTestEnvironment`（第 242-249 行）改为：

```go
type ServerTestEnvironment struct {
	ProbeMethod    string `json:"probe_method"`
	Degraded       bool   `json:"degraded"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	Sandbox        string `json:"sandbox"`
	SandboxReason  string `json:"sandbox_reason,omitempty"`
	Privileges     string `json:"privileges"`
	IPv6Available  bool   `json:"ipv6_available"`
}
```

- [ ] **Step 6: inspectEnvironment 填充新字段**

`src/agent/internal/servertest/runner.go` 第 149-152 行的 return 改为：

```go
	return shared.ServerTestEnvironment{
		ProbeMethod: probeMethod, Degraded: degraded, DegradedReason: degradedReason,
		Sandbox: sandboxState, SandboxReason: sandboxReason, Privileges: privileges,
		IPv6Available: hasGlobalIPv6(),
	}
```

- [ ] **Step 7: 全量 Go 测试 + 提交**

Run: `go test ./src/agent/... ./src/shared/...`
Expected: PASS

```bash
git add src/shared/server_testing.go src/agent/internal/servertest/ipv6.go src/agent/internal/servertest/ipv6_test.go src/agent/internal/servertest/runner.go
git commit -m "feat(agent): report ipv6 availability in test environment"
```

---

### Task 2: 前端类型与纯逻辑助手

**Files:**
- Modify: `src/frontend/src/lib/types.ts:232-239`（`ServerTestEnvironment` 增加可选字段）
- Create: `src/frontend/src/lib/server-test-ipv6.ts`
- Create: `src/frontend/src/lib/server-test-ipv6.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `shared.ServerTestEnvironment.IPv6Available`（JSON 字段 `ipv6_available`）。
- Produces: `IPV6_CATEGORIES: ServerTestCategory[]`；`isIpv6Category(category: ServerTestCategory): boolean`；`ipv6Unavailable(environment: ServerTestEnvironment | null | undefined): boolean`；`withoutIpv6Categories<T extends { category: ServerTestCategory }>(items: T[]): T[]`。Task 3-5 全部使用这四个导出。

- [ ] **Step 1: types.ts 增加可选字段**

`src/frontend/src/lib/types.ts` 的 `ServerTestEnvironment` 接口（第 232-239 行）改为：

```ts
export interface ServerTestEnvironment {
  probe_method: string
  degraded: boolean
  degraded_reason?: string
  sandbox: string
  sandbox_reason?: string
  privileges: string
  ipv6_available?: boolean
}
```

- [ ] **Step 2: 编写失败测试**

创建 `src/frontend/src/lib/server-test-ipv6.test.ts`：

```ts
import { describe, expect, it } from 'vitest'

import { IPV6_CATEGORIES, isIpv6Category, ipv6Unavailable, withoutIpv6Categories } from '@/lib/server-test-ipv6'
import type { ServerTestCategoryResult, ServerTestEnvironment } from '@/lib/types'

describe('server-test-ipv6', () => {
  it('ipv6Unavailable only for explicit false', () => {
    expect(ipv6Unavailable(undefined)).toBe(false)
    expect(ipv6Unavailable(null)).toBe(false)
    expect(ipv6Unavailable({} as ServerTestEnvironment)).toBe(false)
    expect(ipv6Unavailable({ ipv6_available: true } as ServerTestEnvironment)).toBe(false)
    expect(ipv6Unavailable({ ipv6_available: false } as ServerTestEnvironment)).toBe(true)
  })

  it('isIpv6Category covers the three ipv6 categories only', () => {
    for (const category of IPV6_CATEGORIES) {
      expect(isIpv6Category(category)).toBe(true)
    }
    expect(isIpv6Category('tcp_ipv4')).toBe(false)
    expect(isIpv6Category('ip_quality')).toBe(false)
  })

  it('withoutIpv6Categories filters ipv6 categories and keeps others', () => {
    const categories: ServerTestCategoryResult[] = [
      { category: 'ip_quality', status: 'available' },
      { category: 'tcp_ipv4', status: 'available' },
      { category: 'tcp_ipv6', status: 'unavailable' },
      { category: 'return_route_ipv6', status: 'unavailable' },
    ]
    const kept = withoutIpv6Categories(categories).map((item) => item.category)
    expect(kept).toEqual(['ip_quality', 'tcp_ipv4'])
  })
})
```

- [ ] **Step 3: 运行测试确认失败**

Run（`src/frontend` 目录）: `npx vitest run src/lib/server-test-ipv6.test.ts`
Expected: FAIL —— 找不到模块 `@/lib/server-test-ipv6`

- [ ] **Step 4: 实现助手模块**

创建 `src/frontend/src/lib/server-test-ipv6.ts`：

```ts
import type { ServerTestCategory, ServerTestEnvironment } from '@/lib/types'

export const IPV6_CATEGORIES: ServerTestCategory[] = ['tcp_ipv6', 'cernet2_ipv6', 'return_route_ipv6']

export function isIpv6Category(category: ServerTestCategory): boolean {
  return IPV6_CATEGORIES.includes(category)
}

export function ipv6Unavailable(environment: ServerTestEnvironment | null | undefined): boolean {
  return environment?.ipv6_available === false
}

export function withoutIpv6Categories<T extends { category: ServerTestCategory }>(items: T[]): T[] {
  return items.filter((item) => !isIpv6Category(item.category))
}
```

- [ ] **Step 5: 运行测试确认通过 + lint/typecheck**

Run: `npx vitest run src/lib/server-test-ipv6.test.ts && npm run lint && npx tsc -b`
Expected: 全部 PASS / 无告警

- [ ] **Step 6: 提交**

```bash
git add src/frontend/src/lib/types.ts src/frontend/src/lib/server-test-ipv6.ts src/frontend/src/lib/server-test-ipv6.test.ts
git commit -m "feat(panel): add ipv6 availability helpers"
```

---

### Task 3: 报告弹窗隐藏 IPv6 章节并提示

**Files:**
- Modify: `src/frontend/src/components/ServerTestPanel.tsx`（import 区、`TestReport` 函数第 307-321 行）

**Interfaces:**
- Consumes: Task 2 的 `ipv6Unavailable`、`withoutIpv6Categories`。
- Produces: `TestReport` 在 `report.environment.ipv6_available === false` 时过滤 IPv6 章节并渲染提示行。

- [ ] **Step 1: 加入 import**

`src/frontend/src/components/ServerTestPanel.tsx` 第 29 行（`import { cn } from '@/lib/utils'`）之后新增：

```ts
import { ipv6Unavailable, withoutIpv6Categories } from '@/lib/server-test-ipv6'
```

- [ ] **Step 2: 修改 TestReport**

`TestReport`（第 307-321 行）整体替换为：

```tsx
function TestReport({ report, timezone }: { report: ServerTestReport; timezone?: string }) {
  const ipv6Off = ipv6Unavailable(report.environment)
  const visibleCategories = ipv6Off ? withoutIpv6Categories(report.categories) : report.categories
  return (
    <div className="min-w-0 space-y-4">
      <div className="grid gap-3 border-y py-3 text-xs sm:grid-cols-4">
        <div><span className="block text-muted-foreground">状态</span><span className="mt-1 block font-medium">{statusLabel(report.status)}</span></div>
        <div><span className="block text-muted-foreground">完成时间</span><span className="mt-1 block">{formatDateTime(report.completed_at, timezone)}</span></div>
        <div><span className="block text-muted-foreground">Agent</span><span className="mt-1 block font-mono">{report.agent_version}</span></div>
        <div><span className="block text-muted-foreground">权限 / 沙箱</span><span className="mt-1 block">{report.environment.privileges} · {report.environment.sandbox}</span></div>
      </div>
      {report.environment.degraded || report.environment.sandbox_reason ? <div className="flex items-start gap-2 bg-warning/10 px-3 py-2 text-xs text-warning"><AlertTriangleIcon className="mt-0.5 size-3.5 shrink-0" /><span>{report.environment.degraded_reason || report.environment.sandbox_reason}</span></div> : null}
      {ipv6Off ? <div className="flex items-start gap-2 bg-muted/40 px-3 py-2 text-xs text-muted-foreground"><WifiIcon className="mt-0.5 size-3.5 shrink-0" /><span>IPv6 不可用，IPv6 相关章节已隐藏</span></div> : null}
      <ErrorNotice code={report.error_code} message={report.error_message} />
      <div>{visibleCategories.map((category) => <ReportCategory key={category.category} category={category} />)}</div>
    </div>
  )
}
```

- [ ] **Step 3: 类型检查 + lint**

Run: `npx tsc -b && npm run lint`
Expected: 无错误无告警

- [ ] **Step 4: 手工验证**

Run: `npm run dev`，打开任意跑过测试的服务器详情页 → 查看测试结果：
- 报告无 `ipv6_available` 字段（旧报告）→ 章节全部照常显示（回归验证）。
- 在面板数据库 `server_test_tasks` 表的 `result` JSON 中临时加入 `"ipv6_available": false`（或本地构造）→ 报告不出现 IPv6 TCP / 教育网 IPv6 / IPv6 回程 三个章节，出现「IPv6 不可用，IPv6 相关章节已隐藏」提示行；其余章节正常。

- [ ] **Step 5: 提交**

```bash
git add src/frontend/src/components/ServerTestPanel.tsx
git commit -m "feat(panel): hide ipv6 sections in test report when unavailable"
```

---

### Task 4: 选择弹窗置灰 IPv6 选项

**Files:**
- Modify: `src/frontend/src/components/ServerTestPanel.tsx`（`ServerTestPanel` 组件：import 区、`openSelection` 第 373-382 行、`toggleCategory` 第 384-395 行、选项渲染第 458 行、警告弹窗文案第 471-476 行）

**Interfaces:**
- Consumes: Task 2 的 `isIpv6Category`、`ipv6Unavailable`。
- Produces: 组件内 `ipv6Off` 布尔（来自 `task?.result?.environment`）；选择弹窗对 IPv6 选项置灰 + 默认取消勾选 + 确认后可勾选。

- [ ] **Step 1: 加入 import 与组件级信号**

import（第 29 行 `cn` 之后）：

```ts
import { isIpv6Category, ipv6Unavailable } from '@/lib/server-test-ipv6'
```

`ServerTestPanel` 组件内，`const [error, setError] = useState('')`（第 336 行）之后新增：

```ts
  const ipv6Off = ipv6Unavailable(task?.result?.environment)
```

- [ ] **Step 2: openSelection 默认剔除 IPv6 类别**

`openSelection`（第 373-375 行）替换为：

```ts
  const openSelection = () => {
    const base = server.machine_type === 'nat' ? (['ip_quality'] as ServerTestCategory[]) : directDefaults
    setSelected(ipv6Off ? base.filter((category) => !isIpv6Category(category)) : base)
    setError('')
    setSelectionOpen(true)
```

- [ ] **Step 3: toggleCategory 增加 IPv6 确认流程**

`toggleCategory`（第 384-395 行）整体替换为：

```ts
  const toggleCategory = (category: ServerTestCategory, checked: boolean) => {
    if (!checked) {
      setSelected((current) => current.filter((item) => item !== category))
      return
    }
    if ((server.machine_type === 'nat' && category !== 'ip_quality') || category === 'speed' || (ipv6Off && isIpv6Category(category))) {
      setPendingCategory(category)
      setWarningOpen(true)
      return
    }
    setSelected((current) => [...current, category])
  }
```

- [ ] **Step 4: 选项渲染置灰**

第 458 行的选项渲染（`{categoryOptions.filter(...).map(...)}` 整段）替换为：

```tsx
<div className="grid gap-2 sm:grid-cols-2">{categoryOptions.filter((option) => option.group === group).map((option) => {
  const greyed = ipv6Off && isIpv6Category(option.category)
  return (
    <label key={option.category} className={cn('flex cursor-pointer items-start gap-3 border px-3 py-2.5 transition-colors hover:bg-muted/40', selected.includes(option.category) && 'border-primary/40 bg-primary/[0.04]', greyed && 'opacity-60')}>
      <input type="checkbox" className="mt-0.5 size-4 accent-primary" checked={selected.includes(option.category)} onChange={(event) => toggleCategory(option.category, event.target.checked)} />
      <span className="min-w-0"><span className="block text-sm font-medium">{option.label}</span><span className="mt-0.5 block text-xs text-muted-foreground">{option.description}</span></span>
    </label>
  )
})}</div>
```

- [ ] **Step 5: 警告弹窗文案覆盖 IPv6 场景**

第 473 行的 `DialogDescription` 内容替换为：

```tsx
<DialogDescription>{pendingCategory && isIpv6Category(pendingCategory) && ipv6Off ? '该服务器未检测到 IPv6 地址，相关测试可能全部失败。' : server.machine_type === 'nat' && pendingCategory !== 'ip_quality' ? 'NAT 机型的 TCP、回程或测速测试可能因系统、端口映射或运营商限制而不可用。' : '该项目会产生大量网络流量。'}{pendingCategory === 'speed' ? ' 单线程测速最多可能消耗约 5.5 GiB 流量。' : ''}</DialogDescription>
```

- [ ] **Step 6: 类型检查 + lint + 手工验证**

Run: `npx tsc -b && npm run lint`
Expected: 无错误无告警

手工验证（dev server）：
- 对最近报告带 `"ipv6_available": false` 的服务器点「运行测试」→ 选择弹窗中 IPv6 三个选项置灰（半透明）、默认未勾选；点击其中一个 → 弹确认提示「该服务器未检测到 IPv6 地址，相关测试可能全部失败」→ 确认后勾选生效。
- 对最近报告无该字段（或 `true`）的服务器 → 选项正常、默认勾选不变（回归）。

- [ ] **Step 7: 提交**

```bash
git add src/frontend/src/components/ServerTestPanel.tsx
git commit -m "feat(panel): grey out ipv6 test options when unavailable"
```

---

### Task 5: 进度弹窗隐藏 IPv6 条目

**Files:**
- Modify: `src/frontend/src/components/ServerTestPanel.tsx`（组件体 `ipv6Off` 之后插入常量；进度弹窗渲染，第 483 行）

**Interfaces:**
- Consumes: Task 2 的 `withoutIpv6Categories`；Task 4 的组件级 `ipv6Off`。
- Produces: 进度弹窗在 `ipv6Off` 时不渲染 IPv6 类别行。

- [ ] **Step 1: 组件体内定义过滤后的进度行**

`ServerTestPanel` 组件内，Task 4 新增的 `const ipv6Off = ipv6Unavailable(task?.result?.environment)` 之后新增：

```ts
  const progressRows = task?.progress?.categories ?? task?.categories.map((category) => ({ category, status: 'pending', completed: 0, total: 1, message: '' })) ?? []
  const visibleProgressRows = ipv6Off ? withoutIpv6Categories(progressRows) : progressRows
```

- [ ] **Step 2: 进度弹窗改用 visibleProgressRows**

第 483 行（进度弹窗的 `.map((progress) => ...)`）整行替换为：

```tsx
<div className="divide-y border-y">{visibleProgressRows.map((progress) => <div key={progress.category} className="flex items-center gap-3 py-3"><span className={cn('flex size-7 shrink-0 items-center justify-center border', progress.status === 'running' && 'border-info text-info', ['available', 'limited', 'succeeded'].includes(progress.status) && 'border-success text-success', ['unavailable', 'failed'].includes(progress.status) && 'border-destructive text-destructive')}>{progress.status === 'running' ? <LoaderCircleIcon className="size-3.5 animate-spin motion-reduce:animate-none" /> : ['available', 'limited', 'succeeded'].includes(progress.status) ? <CheckIcon className="size-3.5" /> : ['unavailable', 'failed'].includes(progress.status) ? <XCircleIcon className="size-3.5" /> : <CircleDotIcon className="size-3.5" />}</span><div className="min-w-0 flex-1"><div className="flex items-center justify-between gap-3 text-xs"><span className="font-medium">{categoryLabels[progress.category]}</span><span className="tabular-nums text-muted-foreground">{progress.completed}/{progress.total}</span></div>{progress.message ? <p className="mt-1 truncate text-xs text-muted-foreground">{progress.message}</p> : null}</div></div>)}</div>
```

- [ ] **Step 3: 类型检查 + lint**

Run: `npx tsc -b && npm run lint`
Expected: 无错误无告警

- [ ] **Step 4: 手工验证**

Run: `npm run dev`：
- 对 `ipv6_available: false` 的服务器下发包含 IPv6 类别的测试（手动确认勾选后）→ 进度弹窗不显示 IPv6 行，其余行正常；报告弹窗按 Task 3 行为渲染。
- 对 `ipv6_available: true` 或旧报告的服务器 → 进度弹窗照常显示所有类别（回归）。

- [ ] **Step 5: 提交**

```bash
git add src/frontend/src/components/ServerTestPanel.tsx
git commit -m "feat(panel): hide ipv6 rows in test progress when unavailable"
```

---

### 完成验证

- Run: `go test ./src/agent/... ./src/shared/...`（仓库根）→ PASS
- Run（`src/frontend`）: `npm run lint && npx tsc -b && npm test` → 无错误全通过
- 三个弹窗在信号 未知/true/false 三种状态下手工回归通过（对应 Task 3-5 的手工验证步骤）。
