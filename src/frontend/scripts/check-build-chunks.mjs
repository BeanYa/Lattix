import { readFile, readdir } from 'node:fs/promises'
import { resolve } from 'node:path'

const assetsDirectory = resolve(import.meta.dirname, '../dist/assets')
const assetNames = await readdir(assetsDirectory)
const jsAssets = assetNames.filter((name) => name.endsWith('.js'))
const dependencies = new Map()

for (const assetName of jsAssets) {
  const source = await readFile(resolve(assetsDirectory, assetName), 'utf8')
  const imports = new Set()
  const staticImportPattern = /(?:\bfrom|\bimport)\s*["']\.\/([^"']+)["']/g
  for (const match of source.matchAll(staticImportPattern)) {
    if (jsAssets.includes(match[1])) imports.add(match[1])
  }
  dependencies.set(assetName, imports)
}

const visited = new Set()
const active = new Set()
const path = []

function findCycle(assetName) {
  if (active.has(assetName)) return [...path.slice(path.indexOf(assetName)), assetName]
  if (visited.has(assetName)) return null

  visited.add(assetName)
  active.add(assetName)
  path.push(assetName)
  for (const dependency of dependencies.get(assetName) ?? []) {
    const cycle = findCycle(dependency)
    if (cycle) return cycle
  }
  path.pop()
  active.delete(assetName)
  return null
}

for (const assetName of jsAssets) {
  const cycle = findCycle(assetName)
  if (cycle) throw new Error(`Circular build chunks: ${cycle.join(' -> ')}`)
}
