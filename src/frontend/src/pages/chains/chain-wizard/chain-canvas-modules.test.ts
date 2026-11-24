import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import ts from 'typescript'
import { expect, it } from 'vitest'

it('initializes canvas and editor geometry on a cold native ESM canvas import', () => {
  const directory = mkdtempSync(join(tmpdir(), 'lattix-canvas-esm-'))
  const modules = {
    'chain-canvas': new URL('./chain-canvas.ts', import.meta.url),
    'orchestration-graph': new URL('../orchestration/orchestration-graph.ts', import.meta.url),
    'orchestration-draft': new URL('../orchestration/orchestration-draft.ts', import.meta.url),
    'server-state': new URL('../../../lib/server-state.ts', import.meta.url),
  }
  try {
    for (const [name, url] of Object.entries(modules)) {
      const output = ts.transpileModule(readFileSync(url, 'utf8'), {
        compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
      }).outputText
      const linked = output.replace(
        /(['"])(?:\.\.\/orchestration\/|\.\.\/chain-wizard\/|\.\/|@\/lib\/)(chain-canvas|orchestration-graph|orchestration-draft|server-state)\1/g,
        "'./$2.mjs'",
      )
      writeFileSync(join(directory, `${name}.mjs`), linked)
    }
    const canvasUrl = pathToFileURL(join(directory, 'chain-canvas.mjs')).href
    const graphUrl = pathToFileURL(join(directory, 'orchestration-graph.mjs')).href
    const output = execFileSync(
      'node',
      [
        '--input-type=module',
        '--eval',
        `const canvas = await import(${JSON.stringify(canvasUrl)}); const graph = await import(${JSON.stringify(graphUrl)}); console.log(canvas.CARD_W, graph.CARD_X);`,
      ],
      { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] },
    )
    expect(output.trim()).toBe('264 344')
  } finally {
    rmSync(directory, { recursive: true, force: true })
  }
})
