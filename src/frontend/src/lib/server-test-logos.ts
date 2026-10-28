/**
 * 服务器测试报告的品牌图标与展示名映射。图标打包在 public/logos/ 下，
 * 由浏览器直接加载，不依赖第三方 CDN；缺失图标时渲染首字母占位。
 */

/** 网络连通性站点图标，键为站点 slug（target ID 的第三段）。 */
export const connectivitySiteLogos: Record<string, string> = {
  deepseek: '/logos/connectivity/deepseek.ico',
  douyin: '/logos/connectivity/douyin.ico',
  bilibili: '/logos/connectivity/bilibili.ico',
  jd: '/logos/connectivity/jd.ico',
  qq: '/logos/connectivity/qq.webp',
  weixin: '/logos/connectivity/weixin.webp',
  xiaohongshu: '/logos/connectivity/xiaohongshu.ico',
  weibo: '/logos/connectivity/weibo.ico',
  baidu: '/logos/connectivity/baidu.ico',
  netease: '/logos/connectivity/netease.png',
  taobao: '/logos/connectivity/taobao.webp',
  mi: '/logos/connectivity/mi.ico',
  sony: '/logos/connectivity/sony.ico',
  nintendo: '/logos/connectivity/nintendo.ico',
  yahoojp: '/logos/connectivity/yahoojp.ico',
  line: '/logos/connectivity/line.png',
  apple: '/logos/connectivity/apple.ico',
  google: '/logos/connectivity/google.ico',
  youtube: '/logos/connectivity/youtube.webp',
  github: '/logos/connectivity/github.webp',
  cloudflare: '/logos/connectivity/cloudflare.webp',
  claude: '/logos/connectivity/claude.webp',
  chatgpt: '/logos/connectivity/chatgpt.webp',
  gemini: '/logos/connectivity/gemini.png',
  amazon: '/logos/connectivity/amazon.ico',
  bing: '/logos/connectivity/bing.ico',
  steam: '/logos/connectivity/steam.ico',
  oracle: '/logos/connectivity/oracle.ico',
  zoom: '/logos/connectivity/zoom.webp',
  facebook: '/logos/connectivity/facebook.ico',
  instagram: '/logos/connectivity/instagram.png',
  x: '/logos/connectivity/x.webp',
  reddit: '/logos/connectivity/reddit.png',
  linkedin: '/logos/connectivity/linkedin.ico',
  twitch: '/logos/connectivity/twitch.ico',
  netflix: '/logos/connectivity/netflix.ico',
  adobe: '/logos/connectivity/adobe.svg',
  awssts: '/logos/connectivity/aws.svg',
  awsstatic: '/logos/connectivity/aws.svg',
  discord: '/logos/connectivity/discord.svg',
  dropbox: '/logos/connectivity/dropbox.svg',
  gitlab: '/logos/connectivity/gitlab.svg',
  gmail: '/logos/connectivity/gmail.svg',
  icloud: '/logos/connectivity/icloud.svg',
  microsoft: '/logos/connectivity/microsoft.ico',
  notion: '/logos/connectivity/notion.svg',
  openai: '/logos/connectivity/openai.svg',
  paypal: '/logos/connectivity/paypal.svg',
  slack: '/logos/connectivity/slack.svg',
  akamai: '/logos/connectivity/akamai.svg',
  fastly: '/logos/connectivity/fastly.svg',
  jsdelivr: '/logos/connectivity/jsdelivr.svg',
  vercel: '/logos/connectivity/vercel.svg',
  gfonts: '/logos/connectivity/google.ico',
  googleapis: '/logos/connectivity/google.ico',
  gstatic: '/logos/connectivity/google.ico',
  aspnetcdn: '/logos/connectivity/microsoft.ico',
  telegram: '/logos/connectivity/telegram.svg',
  tiktok: '/logos/connectivity/tiktok.png',
  spotify: '/logos/connectivity/spotify.ico',
  npm: '/logos/connectivity/npm.png',
  takealot: '/logos/connectivity/takealot.png',
  pixpix: '/logos/connectivity/pixpix.ico',
  naver: '/logos/connectivity/naver.ico',
  noon: '/logos/connectivity/noon.png',
  wikipedia: '/logos/connectivity/wikipedia.ico',
  bbc: '/logos/connectivity/bbc.ico',
  mistral: '/logos/connectivity/mistral.ico',
  yandex: '/logos/connectivity/yandex.ico',
  mercadolibre: '/logos/connectivity/mercadolibre.ico',
}

export function connectivitySiteLogo(slug: string): string | undefined {
  return connectivitySiteLogos[slug]
}

/** 从 target ID（connectivity:<group>:<slug>）提取站点 slug。 */
export function connectivitySiteSlug(id: string): string {
  const [, , slug] = id.split(':')
  return slug ?? ''
}

/** 流媒体厂商图标，键为归一化厂商名（小写、仅字母数字）。 */
const mediaVendorLogos: Record<string, string> = {
  tiktok: '/logos/media/tiktok.png',
  disneyplus: '/logos/media/disneyplus.svg',
  netflix: '/logos/media/netflix.ico',
  youtube: '/logos/media/youtube.webp',
  amazonprimevideo: '/logos/media/amazonpv.svg',
  reddit: '/logos/media/reddit.png',
  chatgpt: '/logos/media/chatgpt.webp',
  spotify: '/logos/media/spotify.ico',
}

/** 厂商展示顺序（仿 NodeQuality），未收录的厂商按字母序追加在后。 */
const mediaVendorOrder = [
  'tiktok',
  'disneyplus',
  'netflix',
  'youtube',
  'amazonprimevideo',
  'reddit',
  'chatgpt',
  'spotify',
]

const mediaVendorNames: Record<string, string> = {
  tiktok: 'TikTok',
  disneyplus: 'Disney+',
  netflix: 'Netflix',
  youtube: 'YouTube',
  amazonprimevideo: 'Amazon Prime Video',
  reddit: 'Reddit',
  chatgpt: 'ChatGPT',
  spotify: 'Spotify',
}

/** mediaVendorKey 归一化脚本输出的厂商键（"Disney+" → "disneyplus"）。 */
export function mediaVendorKey(name: string): string {
  return name
    .toLowerCase()
    .replace(/[\s_-]+/g, '')
    .replace(/\+/g, 'plus')
    .replace(/[^a-z0-9]/g, '')
}

export function mediaVendorLogo(key: string): string | undefined {
  return mediaVendorLogos[key]
}

export function mediaVendorDisplayName(key: string, rawName: string): string {
  return mediaVendorNames[key] ?? rawName
}

/**
 * orderedMediaVendors 输出按固定顺序排列的厂商键；同一厂商以不同原始键出现
 * （如 "DisneyPlus" 与 "Disney+"）时保留数据最完整的一条。
 */
export function orderedMediaVendors<T extends { Status?: string; Region?: string; Type?: string }>(
  media: Record<string, T>,
): Array<{ key: string; rawName: string; status: T }> {
  const byKey = new Map<string, { key: string; rawName: string; status: T }>()
  for (const [rawName, status] of Object.entries(media)) {
    const key = mediaVendorKey(rawName)
    const existing = byKey.get(key)
    if (!existing || mediaFilled(status) > mediaFilled(existing.status)) {
      byKey.set(key, { key, rawName, status })
    }
  }
  return Array.from(byKey.values()).sort((left, right) => {
    const leftIndex = mediaVendorOrder.indexOf(left.key)
    const rightIndex = mediaVendorOrder.indexOf(right.key)
    if (leftIndex !== -1 || rightIndex !== -1) {
      if (leftIndex === -1) return 1
      if (rightIndex === -1) return -1
      return leftIndex - rightIndex
    }
    return left.rawName.localeCompare(right.rawName)
  })
}

function mediaFilled(status: { Status?: string; Region?: string; Type?: string }): number {
  return [status.Status, status.Region, status.Type].filter((value) => value && value !== '').length
}

/**
 * mediaStatusText 解锁状态文案。上游脚本偶发把 JSON 片段写进 Region/Type，
 * 这里同时做防御性清洗，保证历史脏数据报告也不外泄垃圾字符。
 */
export function mediaStatusText(status: { Status?: string; Region?: string; Type?: string }): {
  label: string
  tone: 'success' | 'warning' | 'destructive' | 'muted'
} {
  const raw = (status.Status ?? '').trim()
  switch (raw) {
    case 'Yes':
      return { label: '解锁', tone: 'success' }
    case 'Restricted':
      return { label: '仅自制', tone: 'warning' }
    case 'No':
    case 'Block':
      return { label: '屏蔽', tone: 'destructive' }
    case 'Unsupported':
      return { label: '不支持', tone: 'muted' }
    default:
      return { label: /^[A-Za-z]{2,16}$/.test(raw) ? raw : '未知', tone: 'muted' }
  }
}

export function mediaRegionText(status: {
  Status?: string
  Region?: string
  Type?: string
}): string {
  const region = (status.Region ?? '').trim()
  return /^[A-Za-z]{2,3}$/.test(region) ? region.toUpperCase() : '-'
}

export function mediaMethodText(status: {
  Status?: string
  Region?: string
  Type?: string
}): string {
  switch ((status.Type ?? '').trim()) {
    case 'Native':
      return '原生'
    case 'DNS':
      return 'DNS'
    default:
      return '-'
  }
}
