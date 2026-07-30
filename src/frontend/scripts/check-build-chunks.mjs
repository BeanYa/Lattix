import { readdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const assetsDirectory = resolve(import.meta.dirname, '../dist/assets')
const assetNames = await readdir(assetsDirectory)
const threeChunks = assetNames.filter((name) => /^three-.*\.js$/.test(name))

if (threeChunks.length !== 1) {
  throw new Error(`Expected one Three.js chunk, found ${threeChunks.length}: ${threeChunks.join(', ')}`)
}

await import(pathToFileURL(resolve(assetsDirectory, threeChunks[0])).href)
