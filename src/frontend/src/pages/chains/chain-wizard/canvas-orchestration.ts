import type { OrchestrationTemplate, Server } from '@/lib/types'
import { draftOrchestrationDTO } from '../orchestration/orchestration-draft'
import { createEditorState, validateGraph } from '../orchestration/orchestration-graph'
import { isExternalServer, nodeServerIds, type CanvasNode } from './chain-canvas'

/** Validate every final hop role, before any request is sent. */
export function validatePathOrchestrations(
  paths: CanvasNode[][],
  servers: Server[],
  templates: OrchestrationTemplate[] = [],
): string[] {
  const serverMap = new Map(servers.map((s) => [s.id, s]))
  const errors: string[] = []
  paths.forEach((path, pathIndex) =>
    path.forEach((node, seq) => {
      const draft = node.orchestration
      if (!draft) return
      const template = templates.find((t) => t.id === draft.template_id)
      const blockedServerIds = new Set(
        path.filter((n) => n.id !== node.id).flatMap((n) => nodeServerIds(n, templates)),
      )
      const state = createEditorState({
        chainId: 0,
        seq,
        activeServerId: node.serverId,
        orchestration: draftOrchestrationDTO(draft, servers, node.serverId, template),
      })
      for (const error of validateGraph(state, {
        validateRootParameters: true,
        seq,
        totalHops: path.length,
        hopRole:
          path.length === 1 || seq === path.length - 1 ? 'exit' : seq === 0 ? 'entry' : 'middle',
        primaryIsExternal: serverMap.get(state.rootServerId)
          ? isExternalServer(serverMap.get(state.rootServerId)!)
          : undefined,
        serverMap,
        blockedServerIds,
      }))
        errors.push(`链路 ${pathIndex + 1} 第 ${seq + 1} 跳：${error}`)
    }),
  )
  return errors
}
