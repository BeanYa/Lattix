import type { PortRange } from './types'

// NAT 可用端口段的文本写法（§21）：单端口 10000 / 范围 10001-10010 /
// 非 1:1 映射 20001-20010:10001-10010（外部段:内部段）。与 shared/ports.go 语义保持一致。

/** 解析一行端口段文本，非法返回 null。 */
export function parsePortRange(text: string): PortRange | null {
  const t = text.trim()
  if (!t) {
    return null
  }
  const parseNum = (s: string): number | null => {
    if (!/^\d+$/.test(s)) {
      return null
    }
    const n = Number(s)
    return n >= 1 && n <= 65535 ? n : null
  }
  const parseSide = (s: string): [number, number] | null => {
    const parts = s.split('-')
    if (parts.length > 2) {
      return null
    }
    const start = parseNum(parts[0])
    const end = parts.length === 2 ? parseNum(parts[1]) : start
    if (start === null || end === null || start > end) {
      return null
    }
    return [start, end]
  }
  const sides = t.split(':')
  if (sides.length > 2) {
    return null
  }
  const pub = parseSide(sides[0])
  if (!pub) {
    return null
  }
  if (sides.length === 1) {
    return { pub_start: pub[0], pub_end: pub[1] }
  }
  const listen = parseSide(sides[1])
  if (!listen) {
    return null
  }
  return { pub_start: pub[0], pub_end: pub[1], listen_start: listen[0], listen_end: listen[1] }
}

/** 格式化为文本写法（编辑表单回填用）。 */
export function formatPortRange(r: PortRange): string {
  const pub = r.pub_start === r.pub_end ? `${r.pub_start}` : `${r.pub_start}-${r.pub_end}`
  if (r.listen_start === undefined || r.listen_end === undefined) {
    return pub
  }
  const listen =
    r.listen_start === r.listen_end ? `${r.listen_start}` : `${r.listen_start}-${r.listen_end}`
  return `${pub}:${listen}`
}

function overlaps(s1: number, e1: number, s2: number, e2: number): boolean {
  return s1 <= e2 && s2 <= e1
}

/** 校验端口段列表（与后端 ValidatePortRanges 同规则），返回错误文案，合法返回 ''。 */
export function validatePortRanges(ranges: PortRange[]): string {
  for (let i = 0; i < ranges.length; i++) {
    const r = ranges[i]
    if (r.pub_start < 1 || r.pub_end > 65535 || r.pub_start > r.pub_end) {
      return `端口段 ${i + 1} 非法：外部段须 1-65535 且 start<=end`
    }
    const hasListen = r.listen_start !== undefined || r.listen_end !== undefined
    if (hasListen) {
      const ls = r.listen_start ?? 0
      const le = r.listen_end ?? 0
      if (ls < 1 || le > 65535 || ls > le) {
        return `端口段 ${i + 1} 非法：监听段须 1-65535 且 start<=end`
      }
      if (le - ls !== r.pub_end - r.pub_start) {
        return `端口段 ${i + 1} 非法：外部段与监听段宽度不一致`
      }
    }
  }
  const listenSide = (r: PortRange): [number, number] =>
    r.listen_start !== undefined && r.listen_end !== undefined
      ? [r.listen_start, r.listen_end]
      : [r.pub_start, r.pub_end]
  for (let i = 0; i < ranges.length; i++) {
    for (let j = i + 1; j < ranges.length; j++) {
      if (
        overlaps(ranges[i].pub_start, ranges[i].pub_end, ranges[j].pub_start, ranges[j].pub_end)
      ) {
        return `端口段 ${i + 1} 与 ${j + 1} 的外部段重叠`
      }
      const [s1, e1] = listenSide(ranges[i])
      const [s2, e2] = listenSide(ranges[j])
      if (overlaps(s1, e1, s2, e2)) {
        return `端口段 ${i + 1} 与 ${j + 1} 的监听段重叠`
      }
    }
  }
  return ''
}
