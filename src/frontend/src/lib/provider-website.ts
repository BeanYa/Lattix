// 服务商官网打开前的规整：空白视为未设置；缺协议时补 https，避免 window.open 按相对路径跳回本站。
export function providerWebsiteUrl(raw: string | null | undefined): string | null {
  const value = raw?.trim()
  if (!value) return null
  return /^https?:\/\//i.test(value) ? value : `https://${value}`
}
