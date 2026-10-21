#!/usr/bin/env node
/**
 * P0 性能预算门禁（docs/ui-refactor-proposal.md 10.5 / 11.3）：
 * 读取 dist/assets/index-*.css 与 index-*.js，按 gzip 后体积检查首屏预算。
 *
 * 预算：入口 CSS ≤ 72 KB、入口 JS ≤ 90 KB（KB 按 1024 字节计算）。
 * 用法：先构建，再执行 node scripts/check-bundle-budget.mjs（或 bun run check:budget）。
 * 说明：脚本只读取产物，不触发构建；超预算时以退出码 1 阻断。
 */
import { readdir, readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { gzipSync } from 'node:zlib'

const ASSETS_DIRECTORY = resolve(import.meta.dirname, '../dist/assets')
const KIB = 1024
const BUDGETS = [
  { label: '入口 CSS', pattern: /^index-.*\.css$/, extension: '.css', limit: 72 * KIB },
  { label: '入口 JS', pattern: /^index-.*\.js$/, extension: '.js', limit: 90 * KIB },
]

function formatSize(bytes) {
  return bytes.toLocaleString('en-US') + ' B (' + (bytes / KIB).toFixed(2) + ' KB)'
}

async function readAssetNames() {
  try {
    return await readdir(ASSETS_DIRECTORY)
  } catch (error) {
    if (error?.code === 'ENOENT') {
      throw new Error('未找到构建产物目录：' + ASSETS_DIRECTORY + '；请先执行构建')
    }
    throw error
  }
}

async function checkBudget(assetNames, budget) {
  const matches = assetNames.filter((name) => budget.pattern.test(name)).sort()
  if (matches.length === 0) {
    return {
      failures: [`${budget.label}：未找到 index-*${budget.extension} 产物`],
      lines: [],
    }
  }

  const failures = []
  const lines = []
  for (const name of matches) {
    const source = await readFile(resolve(ASSETS_DIRECTORY, name))
    const gzipSize = gzipSync(source, { level: 9 }).byteLength
    const passed = gzipSize <= budget.limit
    const status = passed ? '通过' : '超预算'
    lines.push(
      `  ${status}  ${name}  原始 ${formatSize(source.byteLength)}，gzip ${formatSize(gzipSize)} / 预算 ${budget.limit / KIB} KB`,
    )
    if (!passed) {
      failures.push(
        `${budget.label} ${name} gzip ${(gzipSize / KIB).toFixed(2)} KB，超过预算 ${budget.limit / KIB} KB`,
      )
    }
  }

  return { failures, lines }
}

async function main() {
  const assetNames = await readAssetNames()
  const failures = []

  console.log('Lattix 首屏体积预算（gzip，1 KB = 1024 B）')
  for (const budget of BUDGETS) {
    console.log(`\n${budget.label}：≤ ${budget.limit / KIB} KB`)
    const result = await checkBudget(assetNames, budget)
    for (const line of result.lines) console.log(line)
    failures.push(...result.failures)
  }

  if (failures.length > 0) {
    console.error('\n预算门禁未通过：')
    for (const failure of failures) console.error('  - ' + failure)
    process.exitCode = 1
    return
  }

  console.log('\n预算门禁通过：入口 CSS 与 JS 均在预算内。')
}

main().catch((error) => {
  console.error('预算门禁执行失败：' + (error instanceof Error ? error.message : String(error)))
  process.exitCode = 1
})
