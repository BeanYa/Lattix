import type {
  ChainHop,
  ChainOrchestration,
  HopOrchestrationNodeInput,
  OrchestrationDraft,
  OrchestrationGraph,
  OrchestrationParamOverride,
  OrchestrationTemplate,
  Server,
} from '@/lib/types'
import { isServerOnline } from '@/lib/server-state'
import type { EditorState } from './orchestration-graph'

const clone = <T>(value: T): T => structuredClone(value)

export function draftFromTemplate(template: OrchestrationTemplate): OrchestrationDraft {
  return {
    nodes: template.nodes.map((node) => ({
      ...clone(node),
      ...(node.kind === 'server' ? { address: '', listen_port: node.listen_port ?? 0 } : {}),
    })),
    edges: clone(template.edges),
    template_id: template.id,
    template_revision: template.revision,
    overrides: [],
  }
}

export function draftFromHop(hop: ChainHop): OrchestrationDraft {
  const graph = hop.orchestration
  if (!graph?.nodes.length)
    return {
      nodes: [
        {
          kind: 'server',
          server_id: hop.server_id,
          is_root: true,
          address: hop.address ?? '',
          listen_port: hop.forward_port ?? 0,
        },
      ],
      edges: [],
    }
  const indexes = new Map(graph.nodes.map((node, i) => [node.id, i]))
  return {
    nodes: graph.nodes.map((node) => ({
      kind: node.kind,
      server_id: node.server_id,
      is_root: node.is_root,
      address: node.address,
      listen_port: node.listen_port,
      subject: node.subject,
      action: node.action,
      priority: node.priority,
    })),
    edges: graph.edges.flatMap((edge) => {
      const from = indexes.get(edge.from),
        to = indexes.get(edge.to)
      return from === undefined || to === undefined ? [] : [{ from, to }]
    }),
    ...(graph.template_id
      ? {
          template_id: graph.template_id,
          template_revision: graph.template_source_revision ?? graph.template_revision,
          overrides: clone(graph.overrides ?? []),
        }
      : {}),
  }
}

/** Keep the newest known template base; overrides are resolved separately. */
export function synchronizeOrchestrationDraft(
  draft: OrchestrationDraft,
  template?: OrchestrationTemplate,
): OrchestrationDraft {
  if (
    draft.template_id &&
    template?.id === draft.template_id &&
    template.revision >= (draft.template_revision ?? 0)
  ) {
    return { ...draftFromTemplate(template), overrides: clone(draft.overrides ?? []) }
  }
  return clone(draft)
}

export function resolveOrchestrationDraft(
  draft: OrchestrationDraft,
  template?: OrchestrationTemplate,
): OrchestrationGraph {
  const source = synchronizeOrchestrationDraft(draft, template)
  return {
    nodes: source.nodes.map((node) => {
      const result = clone(node)
      if (node.kind === 'server') {
        const override = draft.template_id
          ? draft.overrides?.find((item) => item.server_id === node.server_id)
          : undefined
        if (override?.address !== undefined) result.address = override.address
        if (override?.listen_port !== undefined) result.listen_port = override.listen_port
      }
      return result
    }),
    edges: clone(source.edges),
  }
}

export function setDraftOverride(
  draft: OrchestrationDraft,
  serverId: number,
  patch: Omit<OrchestrationParamOverride, 'server_id'>,
): OrchestrationDraft {
  const overrides = clone(draft.overrides ?? [])
  const index = overrides.findIndex((item) => item.server_id === serverId)
  if (index < 0) overrides.push({ server_id: serverId, ...patch })
  else overrides[index] = { ...overrides[index], ...patch }
  return { ...clone(draft), overrides }
}

export function resetDraftOverride(
  draft: OrchestrationDraft,
  serverId: number,
  field: 'address' | 'listen_port',
): OrchestrationDraft {
  const overrides = clone(draft.overrides ?? []).flatMap((item) => {
    if (item.server_id !== serverId) return [item]
    delete item[field]
    return item.address === undefined && item.listen_port === undefined ? [] : [item]
  })
  return { ...clone(draft), overrides }
}

export function detachOrchestrationDraft(
  draft: OrchestrationDraft,
  template?: OrchestrationTemplate,
): OrchestrationDraft {
  return resolveOrchestrationDraft(draft, template)
}

export function draftOrchestrationDTO(
  draft: OrchestrationDraft,
  servers: Server[],
  activeServerId: number,
  template?: OrchestrationTemplate,
): ChainOrchestration {
  const current = synchronizeOrchestrationDraft(draft, template)
  const graph = resolveOrchestrationDraft(current)
  const root =
    graph.nodes.find((node) => node.kind === 'server' && node.is_root)?.server_id ?? activeServerId
  return {
    ...graph,
    template_id: draft.template_id,
    template_revision: current.template_revision,
    template_source_revision: current.template_revision,
    overrides: clone(draft.overrides ?? []),
    root_server_id: root,
    active_server_id: activeServerId,
    triggered: root !== activeServerId,
    nodes: graph.nodes.map((node, id) => {
      const server = servers.find((item) => item.id === node.server_id)
      return {
        ...node,
        id,
        priority: node.priority ?? 0,
        active: node.kind === 'server' && node.server_id === activeServerId,
        online: server ? isServerOnline(server) : false,
        banned: false,
        alias: server?.alias,
        country_code: server?.country_code,
        location: server?.location,
        kind_of_server: server?.kind,
      }
    }),
  }
}

/** Serialize every node, including a root-only template (runtime save intentionally clears it). */
export function graphFromEditorState(state: EditorState): OrchestrationGraph {
  const index = new Map(state.nodes.map((node, i) => [node.key, i]))
  return {
    nodes: state.nodes.map((node): HopOrchestrationNodeInput =>
      node.kind === 'server'
        ? {
            kind: 'server',
            server_id: node.serverId,
            is_root: Boolean(node.isRoot),
            address: node.address,
            listen_port: Number(node.listenPort) || 0,
          }
        : {
            kind: 'trigger',
            subject: node.subject,
            action: node.action,
            priority: Number(node.priority) || 0,
          },
    ),
    edges: state.edges.flatMap((edge) => {
      const from = index.get(edge.from),
        to = index.get(edge.to)
      return from === undefined || to === undefined ? [] : [{ from, to }]
    }),
  }
}
