import { useMemo, useState } from 'react'
import type {
  CatalogProbeRow,
  Chain,
  ChainHop,
  OrchestrationDraft,
  OrchestrationTemplate,
  Server,
} from '@/lib/types'
import { Button } from '@/components/ui/button'
import { OrchestrationEditorCore } from './OrchestrationEditor'
import { OrchestrationTemplateEditor } from './OrchestrationTemplateEditor'
import { createEditorState } from './orchestration-graph'
import {
  detachOrchestrationDraft,
  draftFromHop,
  draftOrchestrationDTO,
  graphFromEditorState,
  resetDraftOverride,
  resolveOrchestrationDraft,
  setDraftOverride,
} from './orchestration-draft'

export function DraftOrchestrationEditor({
  chain,
  hop,
  value,
  templates,
  servers,
  probesByServer,
  probeTitle,
  onClose,
  onApply,
  onTemplatesChanged,
}: {
  chain: Chain
  hop: ChainHop
  value?: OrchestrationDraft
  templates: OrchestrationTemplate[]
  servers: Server[]
  probesByServer?: Map<number, CatalogProbeRow[]>
  probeTitle?: string | null
  onClose: () => void
  onApply: (value: OrchestrationDraft) => void
  onTemplatesChanged?: () => void
}) {
  const [draft, setDraft] = useState(() => structuredClone(value ?? draftFromHop(hop)))
  const [revision, setRevision] = useState(0)
  const [editingTemplate, setEditingTemplate] = useState(false)
  const [updatedTemplate, setUpdatedTemplate] = useState<OrchestrationTemplate>()
  const template =
    updatedTemplate?.id === draft.template_id
      ? updatedTemplate
      : templates.find((item) => item.id === draft.template_id)
  const resolved = resolveOrchestrationDraft(draft, template)
  const initialState = useMemo(
    () =>
      createEditorState({
        chainId: chain.id,
        seq: hop.seq,
        activeServerId: hop.server_id,
        orchestration: draftOrchestrationDTO(draft, servers, hop.server_id, template),
      }),
    [chain.id, hop.seq, hop.server_id, draft, servers, template],
  )
  if (editingTemplate && template)
    return (
      <OrchestrationTemplateEditor
        template={template}
        servers={servers}
        probesByServer={probesByServer}
        probeTitle={probeTitle}
        onClose={() => setEditingTemplate(false)}
        onSaved={(saved) => {
          setUpdatedTemplate(saved)
          setEditingTemplate(false)
          setRevision((n) => n + 1)
          onTemplatesChanged?.()
        }}
      />
    )
  return (
    <OrchestrationEditorCore
      key={revision}
      chain={chain}
      hop={hop}
      servers={servers}
      probesByServer={probesByServer}
      probeTitle={probeTitle}
      onClose={onClose}
      onSaved={() => {}}
      config={{
        initialState,
        title: draft.template_id
          ? `共享编排 · ${template?.name ?? `模板 #${draft.template_id}`}`
          : '独立编排草稿',
        saveLabel: '应用编排',
        structureLocked: Boolean(draft.template_id),
        extraDirty: JSON.stringify(draft) !== JSON.stringify(value ?? draftFromHop(hop)),
        actions: draft.template_id ? (
          <div className="orch-template-actions">
            {!template ? (
              <p role="alert">模板不可用；保留当前关联与参数，请刷新模板列表后重试。</p>
            ) : null}
            <Button variant="outline" disabled={!template} onClick={() => setEditingTemplate(true)}>
              编辑共享模板
            </Button>
            <Button
              variant="outline"
              onClick={() => {
                setDraft(detachOrchestrationDraft(draft, template))
                setRevision((n) => n + 1)
              }}
            >
              另存为独立副本
            </Button>
            <details className="orch-inheritance">
              <summary>实例参数 · 继承与覆盖</summary>
              {resolved.nodes
                .filter((node) => node.kind === 'server')
                .map((node) => {
                  const override = draft.overrides?.find(
                    (item) => item.server_id === node.server_id,
                  )
                  return (
                    <div key={node.server_id}>
                      <strong>
                        {servers.find((server) => server.id === node.server_id)?.alias ??
                          `#${node.server_id}`}
                      </strong>
                      {(['address', 'listen_port'] as const).map((field) => (
                        <span key={field}>
                          {field === 'address' ? '地址' : '端口'}：
                          {override?.[field] === undefined ? '继承模板' : '已覆盖'}
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={override?.[field] === undefined}
                            onClick={() => {
                              setDraft((current) =>
                                resetDraftOverride(current, node.server_id!, field),
                              )
                              setRevision((n) => n + 1)
                            }}
                          >
                            恢复{field === 'address' ? '地址' : '端口'}继承
                          </Button>
                        </span>
                      ))}
                    </div>
                  )
                })}
            </details>
          </div>
        ) : undefined,
        onParameterChange: (serverId, field, raw) => {
          if (draft.template_id)
            setDraft((current) =>
              setDraftOverride(
                current,
                serverId,
                field === 'address' ? { address: raw } : { listen_port: Number(raw) || 0 },
              ),
            )
        },
        onApply: (state) => {
          if (draft.template_id)
            onApply({
              ...structuredClone(draft),
              ...graphFromEditorState(state),
              template_revision: template?.revision ?? draft.template_revision,
            })
          else onApply(graphFromEditorState(state))
        },
      }}
    />
  )
}
