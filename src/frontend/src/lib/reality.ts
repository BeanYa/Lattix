export const DEFAULT_REALITY_DEST = 'dl.google.com'
export const CUSTOM_REALITY_DEST = '__custom__'

// 与后端 destCandidates 保持一致。候选分散在不同厂商网络，实际可用性仍取决于
// 节点所在地区；Agent 部署时会逐个做 TLS 1.3 可达性预检。
export const REALITY_DEST_OPTIONS = [
  { domain: 'dl.google.com', label: 'Google 下载' },
  { domain: 'www.amazon.com', label: 'Amazon' },
  { domain: 'gateway.icloud.com', label: 'iCloud 网关' },
  { domain: 'developer.apple.com', label: 'Apple Developer' },
  { domain: 'cdn.discord.com', label: 'Discord CDN' },
  { domain: 'github.com', label: 'GitHub' },
  { domain: 'www.samsung.com', label: 'Samsung' },
  { domain: 'www.tesla.com', label: 'Tesla' },
  { domain: 'www.bing.com', label: 'Bing' },
  { domain: 'www.yahoo.com', label: 'Yahoo' },
  { domain: 'slack.com', label: 'Slack' },
  { domain: 'yandex.com', label: 'Yandex' },
] as const

export function inferRealityDestPreset(dest: string, serverNames: readonly string[]): string {
  const option = REALITY_DEST_OPTIONS.find(
    ({ domain }) =>
      dest === `${domain}:443` && serverNames.length === 1 && serverNames[0] === domain,
  )
  return option?.domain ?? CUSTOM_REALITY_DEST
}
