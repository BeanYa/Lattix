import { useEffect, useMemo, useState } from 'react'
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
  synchronizeOrchestrationDraft,
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
  onTemplatesChanged?: (saved?: OrchestrationTemplate) => void
}) {
  const [draft, setDraft] = useState(() => structuredClone(value ?? draftFromHop(hop)))
  const [revision, setRevision] = useState(0)
  const [editingTemplate, setEditingTemplate] = useState(false)
  const [updatedTemplate, setUpdatedTemplate] = useState<OrchestrationTemplate>()
  const catalogTemplate = templates.reduce<OrchestrationTemplate | undefined>(
    (latest, item) =>
      item.id === draft.template_id && (!latest || item.revision > latest.revision) ? item : latest,
    updatedTemplate?.id === draft.template_id ? updatedTemplate : undefined,
  )
  useEffect(() => {
    if (
      catalogTemplate &&
      (!updatedTemplate ||
        catalogTemplate.id !== updatedTemplate.id ||
        catalogTemplate.revision > updatedTemplate.revision)
    )
      setUpdatedTemplate(catalogTemplate)
  }, [catalogTemplate, updatedTemplate])
  // A persisted hop snapshot is materialized, so its inherited defaults are
  // unknowable until a catalog at least as new as the binding is available.
  const template =
    catalogTemplate && catalogTemplate.revision >= (draft.template_revision ?? 0)
      ? catalogTemplate
      : undefined
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
          onTemplatesChanged?.(saved)
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
              <div role="alert">
                <p>最新模板默认值尚未就绪；保留当前关联与参数，请刷新模板列表后重试。</p>
                {onTemplatesChanged ? (
                  <Button variant="outline" onClick={() => onTemplatesChanged()}>
                    刷新模板列表
                  </Button>
                ) : null}
              </div>
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
                          {override?.[field] === undefined
                            ? template
                              ? '继承模板'
                              : '等待最新模板默认值'
                            : '已覆盖'}
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!template || override?.[field] === undefined}
                            title={
                              !template ? '最新模板默认值尚未就绪，请刷新后恢复继承' : undefined
                            }
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
          if (draft.template_id) onApply(synchronizeOrchestrationDraft(draft, template))
          else onApply(graphFromEditorState(state))
        },
      }}
    />
  )
}
