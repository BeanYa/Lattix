// xrayVersionAtLeast 三段数字版本比较（hy2 协议选项的版本门控，与后端 shared.XrayMinVersionHy2 同步）。
export const XRAY_MIN_VERSION_HY2 = '26.3.27'

export function xrayVersionAtLeast(
  version: string | null | undefined,
  min: string = XRAY_MIN_VERSION_HY2,
): boolean {
  const parse = (s: string): [number, number, number] | null => {
    const m = /^(\d+)\.(\d+)\.(\d+)/.exec(s.trim())
    return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null
  }
  if (!version) return false
  const a = parse(version)
  const b = parse(min)
  if (!a || !b) return false
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] > b[i]
  }
  return true
}
