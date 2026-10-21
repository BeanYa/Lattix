import {
  ActivityIcon,
  BotIcon,
  CoinsIcon,
  FileCode2Icon,
  FileTextIcon,
  GaugeIcon,
  GlobeIcon,
  KeyRoundIcon,
  LayersIcon,
  LayoutDashboardIcon,
  NetworkIcon,
  RouteIcon,
  ScrollTextIcon,
  ServerIcon,
  SettingsIcon,
  ShieldCheckIcon,
  SlidersHorizontalIcon,
  UserRoundIcon,
  UsersIcon,
  WrenchIcon,
  type LucideIcon,
} from 'lucide-react'

/** 一级入口标识；与 URL 前缀一一对应，供侧栏与命令面板共享。 */
export type PrimaryNavKey = 'board' | 'network' | 'access' | 'run' | 'settings'

export interface NavDestination {
  /** 规范化目标；点击一级入口时直接落到域默认页，避免一次重定向。 */
  to: string
  label: string
  description: string
  icon: LucideIcon
  /** 额外中文/英文别名，仅用于命令面板过滤。 */
  keywords?: string[]
  /** 当前路径命中这些前缀时视为激活；默认使用 to。 */
  activePrefixes?: string[]
}

export interface PrimaryNavItem extends NavDestination {
  key: PrimaryNavKey
  children?: NavDestination[]
}

/**
 * 五个一级入口。顺序即产品中的「值班 → 网络 → 访问 → 运行 → 设置」。
 * to 指向域默认页；域根路径（/network 等）由 App.tsx 做 replace 重定向。
 */
export const PRIMARY_NAV: PrimaryNavItem[] = [
  {
    key: 'board',
    to: '/board',
    label: '控制台',
    description: '现在哪里坏了、影响谁、先做什么',
    icon: LayoutDashboardIcon,
    keywords: ['board', 'dashboard', '值班', '看板', '队列'],
  },
  {
    key: 'network',
    to: '/network/servers',
    label: '网络',
    description: '机群与线路现在是什么形态',
    icon: NetworkIcon,
    keywords: ['network', '网络', '服务器', '链路', '拓扑'],
    children: [
      {
        to: '/network/servers',
        label: '服务器',
        description: '接入状态、漂移与运行指标',
        icon: ServerIcon,
        keywords: ['servers', 'server', '机器', '节点'],
      },
      {
        to: '/network/chains',
        label: '链路',
        description: '直连、中转与独立节点',
        icon: RouteIcon,
        keywords: ['chains', 'chain', '线路', '拓扑', '代理'],
        activePrefixes: ['/network/chains', '/network/nodes'],
      },
      {
        to: '/network/link-groups',
        label: '链路分组',
        description: '把链路编排成可订阅的组合',
        icon: LayersIcon,
        keywords: ['link groups', '分组', '组', '订阅'],
      },
    ],
  },
  {
    key: 'access',
    to: '/access/users',
    label: '访问',
    description: '谁在用、拿到什么订阅、模板怎么发',
    icon: KeyRoundIcon,
    keywords: ['access', '访问', '用户', '订阅', '通行证'],
    children: [
      {
        to: '/access/users',
        label: '用户',
        description: '账号、流量与订阅入口',
        icon: UsersIcon,
        keywords: ['users', 'user', '账号', '订阅'],
      },
      {
        to: '/access/user-groups',
        label: '用户分组',
        description: '按分组批量分配链路',
        icon: UserRoundIcon,
        keywords: ['user groups', '分组', '批量指派'],
      },
      {
        to: '/access/templates',
        label: '订阅模板',
        description: '客户端配置的生成规则',
        icon: FileCode2Icon,
        keywords: ['templates', '模板', '订阅'],
      },
      {
        to: '/access/external-subscriptions',
        label: '外部订阅',
        description: '把外部节点并入本地订阅',
        icon: GlobeIcon,
        keywords: ['external', '外部', '导入', '订阅'],
      },
    ],
  },
  {
    key: 'run',
    to: '/run/console',
    label: '运行',
    description: '面板/Agent 是否健康、钱花在哪、发生过什么',
    icon: GaugeIcon,
    keywords: ['run', '运行时', '监控', '成本', '日志'],
    children: [
      {
        to: '/run/console',
        label: '运行监控',
        description: '面板与 Agent 的实时读数',
        icon: ActivityIcon,
        keywords: ['runtime', 'console', '运行', '监控', '面板'],
      },
      {
        to: '/run/costs',
        label: '成本',
        description: '按机群与周期复盘花费',
        icon: CoinsIcon,
        keywords: ['costs', 'cost', '账单', '费用'],
      },
      {
        to: '/run/logs/operations',
        label: '操作日志',
        description: '变更、失败与重试记录',
        icon: ScrollTextIcon,
        keywords: ['logs', 'operations', '审计', '操作'],
        activePrefixes: ['/run/logs/operations'],
      },
      {
        to: '/run/logs/requests',
        label: '请求日志',
        description: '订阅请求与响应诊断',
        icon: FileTextIcon,
        keywords: ['logs', 'requests', '请求', '订阅'],
      },
    ],
  },
  {
    key: 'settings',
    to: '/settings/agent',
    label: '设置',
    description: '全局策略、证书、告警与维护',
    icon: SettingsIcon,
    keywords: ['settings', '设置', '配置', '系统'],
    children: [
      {
        to: '/settings/agent',
        label: 'Agent',
        description: '接入、证书与版本策略',
        icon: BotIcon,
        keywords: ['agent', '接入', '证书'],
      },
      {
        to: '/settings/runtime',
        label: '运行设置',
        description: '刷新、时区与订阅默认值',
        icon: SlidersHorizontalIcon,
        keywords: ['runtime', '运行', '时区'],
      },
      {
        to: '/settings/security',
        label: '安全通知',
        description: '告警渠道与安全提醒',
        icon: ShieldCheckIcon,
        keywords: ['security', '安全', '告警', '通知'],
      },
      {
        to: '/settings/system',
        label: '系统维护',
        description: '备份、升级与面板维护',
        icon: WrenchIcon,
        keywords: ['system', '维护', '备份', '升级'],
      },
    ],
  },
]

/** 命令面板中的常用页面；包含全部二级入口，保持与侧栏同一数据源。 */
export const COMMON_PAGES: NavDestination[] = PRIMARY_NAV.flatMap((item) => item.children ?? [])

/** 命令面板最终使用的页面索引；不接后端对象搜索。 */
export const COMMAND_PAGES: NavDestination[] = [...PRIMARY_NAV, ...COMMON_PAGES]

function matchesPrefix(location: string, prefix: string): boolean {
  return location === prefix || location.startsWith(prefix + '/')
}

/** 一级入口激活态：/network、/network/servers、/network/chains/12 都算「网络」。 */
export function isPrimaryActive(location: string, item: PrimaryNavItem): boolean {
  return matchesPrefix(location, item.to.split('/').slice(0, 2).join('/'))
}

/** 二级入口激活态；允许配置多个前缀（例如链路与节点详情同属链路）。 */
export function isDestinationActive(location: string, item: NavDestination): boolean {
  const prefixes = item.activePrefixes ?? [item.to]
  return prefixes.some((prefix) => matchesPrefix(location, prefix))
}

/** 当前路径所属的一级入口，用于侧栏只展开当前域。 */
export function activePrimaryNav(location: string): PrimaryNavItem | undefined {
  return PRIMARY_NAV.find((item) => isPrimaryActive(location, item))
}

const EXACT_TITLES = new Map<string, string>([
  ...PRIMARY_NAV.map((item) => [item.to, item.label] as const),
  ...COMMON_PAGES.map((item) => [item.to, item.label] as const),
])

/** 页面标题；路由切换时由 AppShell 写入 document.title。 */
export function pageTitleForPath(location: string): string {
  if (location === '/network/chains/new') return '新建链路'
  if (/^\/network\/chains\/[^/]+\/edit$/.test(location)) return '编辑链路'
  if (/^\/network\/chains\/[^/]+$/.test(location)) return '链路详情'
  if (/^\/network\/nodes\/[^/]+$/.test(location)) return '节点详情'
  if (location.startsWith('/settings/')) {
    const settings = PRIMARY_NAV.find((item) => item.key === 'settings')
    const section = settings?.children?.find((item) => isDestinationActive(location, item))
    return section ? `设置 / ${section.label}` : '设置'
  }
  return EXACT_TITLES.get(location) ?? '页面未找到'
}

/** 命令面板过滤：名称、描述、路径与别名的小写包含匹配。 */
export function filterCommandPages(query: string): NavDestination[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return COMMAND_PAGES
  return COMMAND_PAGES.filter((page) => {
    const haystack = [page.label, page.description, page.to, ...(page.keywords ?? [])]
      .join(' ')
      .toLowerCase()
    return haystack.includes(needle)
  })
}
