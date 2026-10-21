#!/usr/bin/env node
/**
 * P0 令牌对比度门禁（docs/ui-refactor-proposal.md 6.2 / 7.2 / 7.6）：
 * 解析 src/styles/tokens.css 的浅色（:root）与深色（.dark）值，
 * 按 WCAG 2.2 AA 计算正文/状态词对 surface 与 raised 的对比度。
 *
 * 阈值：4.5:1（正文与 12px/600 状态词）；任一令牌不达标即以退出码 1 阻断。
 * 用法：node scripts/check-token-contrast.mjs（或 bun run check:tokens）。
 * 说明：不依赖浏览器与第三方库；只读取令牌文件，不修改任何样式。
 */
import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'

const TOKENS_FILE = resolve(import.meta.dirname, '../src/styles/tokens.css')
const MIN_CONTRAST = 4.5
const BACKGROUNDS = [
  ['surface', '--panel-surface'],
  ['raised', '--panel-raised'],
]
const FOREGROUNDS = [
  ['ink', '--panel-ink'],
  ['ink-2', '--panel-ink-2'],
  ['ink-3', '--panel-ink-3'],
  ['signal', '--panel-signal'],
  ['status-ok', '--panel-status-ok'],
  ['status-progress', '--panel-status-progress'],
  ['status-degraded', '--panel-status-degraded'],
  ['status-failed', '--panel-status-failed'],
  ['status-waiting', '--panel-status-waiting'],
  ['status-unknown', '--panel-status-unknown'],
]

/** 去掉注释后按顶层选择器收集令牌；同一令牌后出现的声明覆盖先出现的声明。 */
function collectThemes(source) {
  const withoutComments = source.replace(/\/\*[\s\S]*?\*\//g, '')
  const themes = { light: new Map(), dark: new Map() }
  const blockPattern = /([^{}]+)\{([^{}]*)\}/g
  let match

  while ((match = blockPattern.exec(withoutComments)) !== null) {
    const selector = match[1].trim()
    if (selector.startsWith('@')) continue

    const selectors = selector.split(',').map((item) => item.trim())
    const isRoot = selectors.includes(':root')
    const isDark = selectors.includes('.dark')
    if (!isRoot && !isDark) continue

    for (const declaration of match[2].split(';')) {
      const separator = declaration.indexOf(':')
      if (separator === -1) continue
      const name = declaration.slice(0, separator).trim()
      if (!name.startsWith('--')) continue
      const value = declaration.slice(separator + 1).trim()
      if (isRoot) themes.light.set(name, value)
      if (isDark) themes.dark.set(name, value)
    }
  }

  return themes
}

/** 解析 var() 别名；复合值（如 color-mix）不在本门禁的检查范围内。 */
function resolveToken(theme, name, stack = []) {
  if (stack.includes(name)) {
    throw new Error('令牌存在循环引用：' + [...stack, name].join(' -> '))
  }
  const value = theme.get(name)
  if (value === undefined) return undefined

  const alias = value.match(/^var\(\s*(--[\w-]+)\s*\)$/)
  if (alias) return resolveToken(theme, alias[1], [...stack, name])
  if (value.includes('var(')) {
    throw new Error('无法解析复合 var() 值：' + name + ': ' + value)
  }
  return value
}

function parseHexColor(hex) {
  const digits = hex.slice(1)
  const expanded =
    digits.length === 3 || digits.length === 4
      ? digits
          .split('')
          .map((digit) => digit + digit)
          .join('')
      : digits
  if (![6, 8].includes(expanded.length) || !/^[\da-f]+$/i.test(expanded)) {
    throw new Error('无法识别的十六进制颜色：' + hex)
  }
  const channel = (index) => Number.parseInt(expanded.slice(index, index + 2), 16)
  const alpha = expanded.length === 8 ? channel(6) / 255 : 1
  return { r: channel(0), g: channel(2), b: channel(4), a: alpha }
}

function parseChannel(value) {
  const text = value.trim()
  if (text.endsWith('%')) return (Number.parseFloat(text) / 100) * 255
  return Number.parseFloat(text)
}

function parseAlpha(value) {
  const text = value.trim()
  if (text.endsWith('%')) return Number.parseFloat(text) / 100
  return Number.parseFloat(text)
}

function parseFunctionalColor(value) {
  const match = value.match(/^(rgba?)\(([^)]+)\)$/i)
  if (!match) return null

  const body = match[2].trim()
  const slashIndex = body.indexOf('/')
  const channelsPart = slashIndex === -1 ? body : body.slice(0, slashIndex)
  const alphaPart = slashIndex === -1 ? null : body.slice(slashIndex + 1).trim()
  const parts = channelsPart.includes(',')
    ? channelsPart.split(',').map((part) => part.trim())
    : channelsPart.split(/\s+/).filter(Boolean)
  if (parts.length < 3) throw new Error('无法识别的 rgb 颜色：' + value)

  const alpha =
    alphaPart !== null ? parseAlpha(alphaPart) : parts.length > 3 ? parseAlpha(parts[3]) : 1
  return {
    r: parseChannel(parts[0]),
    g: parseChannel(parts[1]),
    b: parseChannel(parts[2]),
    a: Math.min(1, Math.max(0, alpha)),
  }
}

function parseColor(value) {
  const text = value.trim().toLowerCase()
  if (text.startsWith('#')) return parseHexColor(text)
  const functional = parseFunctionalColor(text)
  if (functional) return functional
  throw new Error('无法识别的颜色值：' + value)
}

function composite(foreground, background) {
  if (foreground.a >= 1) return foreground
  return {
    r: foreground.r * foreground.a + background.r * (1 - foreground.a),
    g: foreground.g * foreground.a + background.g * (1 - foreground.a),
    b: foreground.b * foreground.a + background.b * (1 - foreground.a),
    a: 1,
  }
}

function relativeLuminance(color) {
  const linear = (channel) => {
    const value = channel / 255
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * linear(color.r) + 0.7152 * linear(color.g) + 0.0722 * linear(color.b)
}

function contrastRatio(foreground, background) {
  const composed = composite(foreground, background)
  const lighter = Math.max(relativeLuminance(composed), relativeLuminance(background))
  const darker = Math.min(relativeLuminance(composed), relativeLuminance(background))
  return (lighter + 0.05) / (darker + 0.05)
}

function themeLabel(themeName) {
  return themeName === 'light' ? '浅色（:root）' : '深色（.dark）'
}

async function main() {
  const source = await readFile(TOKENS_FILE, 'utf8')
  const themes = collectThemes(source)
  const failures = []
  let checks = 0

  console.log('Lattix 令牌对比度门禁（WCAG 2.2 AA，阈值 ' + MIN_CONTRAST + ':1）')
  console.log('令牌文件：' + TOKENS_FILE)

  for (const themeName of ['light', 'dark']) {
    const theme = themes[themeName]
    console.log('\n' + themeLabel(themeName))

    for (const [backgroundLabel, backgroundToken] of BACKGROUNDS) {
      const backgroundValue = resolveToken(theme, backgroundToken)
      if (backgroundValue === undefined) {
        failures.push(themeLabel(themeName) + ' 缺少 ' + backgroundToken)
        continue
      }
      const background = parseColor(backgroundValue)

      for (const [foregroundLabel, foregroundToken] of FOREGROUNDS) {
        const foregroundValue = resolveToken(theme, foregroundToken)
        if (foregroundValue === undefined) {
          failures.push(themeLabel(themeName) + ' 缺少 ' + foregroundToken)
          continue
        }
        const foreground = parseColor(foregroundValue)
        const ratio = contrastRatio(foreground, background)
        const passed = ratio >= MIN_CONTRAST
        checks += 1
        console.log(
          '  ' +
            foregroundLabel.padEnd(18) +
            foregroundValue.padEnd(26) +
            '对 ' +
            backgroundLabel.padEnd(8) +
            ratio.toFixed(2).padStart(6) +
            ':1  ' +
            (passed ? '通过' : '未通过'),
        )
        if (!passed) {
          failures.push(
            themeLabel(themeName) +
              ' ' +
              foregroundLabel +
              ' 对 ' +
              backgroundLabel +
              ' 仅 ' +
              ratio.toFixed(2) +
              ':1，低于 ' +
              MIN_CONTRAST +
              ':1',
          )
        }
      }
    }
  }

  if (failures.length > 0) {
    console.error('\n对比度门禁未通过（共 ' + checks + ' 项检查）：')
    for (const failure of failures) console.error('  - ' + failure)
    process.exitCode = 1
    return
  }

  console.log('\n对比度门禁通过：' + checks + ' 项检查全部 ≥ ' + MIN_CONTRAST + ':1。')
}

main().catch((error) => {
  console.error('对比度门禁执行失败：' + (error instanceof Error ? error.message : String(error)))
  process.exitCode = 1
})
