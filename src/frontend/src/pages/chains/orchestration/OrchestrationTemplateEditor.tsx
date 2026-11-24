import { useMemo, useState } from 'react'
import type {
  CatalogProbeRow,
  Chain,
  ChainHop,
  OrchestrationTemplate,
  OrchestrationTemplateImpact,
  OrchestrationTemplateSaveRequest,
  Server,
} from '@/lib/types'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { OrchestrationEditorCore } from './OrchestrationEditor'
import { createEditorState } from './orchestration-graph'
import {
  draftFromTemplate,
  draftOrchestrationDTO,
  graphFromEditorState,
} from './orchestration-draft'

export function OrchestrationTemplateEditor({
  template,
  servers,
  probesByServer,
  probeTitle,
  onClose,
  onSaved,
}: {
  template?: OrchestrationTemplate
  servers: Server[]
  probesByServer?: Map<number, CatalogProbeRow[]>
  probeTitle?: string | null
  onClose: () => void
  onSaved: (template: OrchestrationTemplate) => void
}) {
  const [name, setName] = useState(template?.name ?? '')
  const [saveAsNew, setSaveAsNew] = useState(false)
  const [pending, setPending] = useState<{
    body: OrchestrationTemplateSaveRequest
    impacts: OrchestrationTemplateImpact[]
  } | null>(null)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const initialState = useMemo(() => {
    const rootId = template?.nodes.find((node) => node.is_root)?.server_id ?? 0
    if (template)
      return createEditorState({
        chainId: 0,
        seq: 0,
        activeServerId: rootId,
        orchestration: draftOrchestrationDTO(
          draftFromTemplate(template),
          servers,
          rootId,
          template,
        ),
      })
    return {
      chainId: 0,
      seq: 0,
      activeServerId: 0,
      rootServerId: 0,
      nodes: [],
      edges: [],
      nextKey: 1,
    }
  }, [template, servers])
  const hop = {
    seq: 0,
    server_id: initialState.rootServerId,
    server_alias: '',
    role: 'entry',
    forward_port: 0,
    address: '',
  } as ChainHop
  const chain = {
    id: 0,
    name: name || '新模板',
    hops: [hop],
    entry_port: 0,
    desired_revision_id: 0,
  } as Chain
  const confirmSave = async () => {
    if (!pending || saving) return
    setSaving(true)
    setError('')
    try {
      onSaved(await api.saveOrchestrationTemplate(pending.body))
    } catch (err) {
      setError(
        err instanceof Error ? err.message : '保存失败，请重试；若模板版本已变化，请关闭后重新打开',
      )
    } finally {
      setSaving(false)
    }
  }
  return (
    <OrchestrationEditorCore
      chain={chain}
      hop={hop}
      servers={servers}
      probesByServer={probesByServer}
      probeTitle={probeTitle}
      onClose={onClose}
      onSaved={() => {}}
      config={{
        initialState,
        templateMode: true,
        structureLocked: Boolean(pending),
        extraDirty: name !== (template?.name ?? '') || saveAsNew,
        title: template ? '编辑共享模板' : '新建编排模板',
        saveLabel: saveAsNew ? '保存为新模板' : '保存模板',
        actions: (
          <div className="orch-template-actions">
            <label>
              模板名称
              <Input
                aria-label="模板名称"
                value={name}
                onChange={(event) => {
                  setName(event.target.value)
                  setPending(null)
                }}
                maxLength={100}
              />
            </label>
            {template ? (
              <label className="orch-inline-check">
                <input
                  type="checkbox"
                  checked={saveAsNew}
                  onChange={(event) => {
                    setSaveAsNew(event.target.checked)
                    setPending(null)
                  }}
                />
                另存为新模板
              </label>
            ) : null}
            {error && !pending ? <p role="alert">{error}</p> : null}
            {pending ? (
              <div className="orch-impact" role="alertdialog" aria-label="确认共享模板影响">
                <div className="orch-impact-card">
                  {error ? <p role="alert">{error}</p> : null}
                  <p>
                    保存「{pending.body.name}」将影响 {pending.impacts.length} 个关联跳位：
                  </p>
                  <ul>
                    {pending.impacts.map((item) => (
                      <li key={`${item.chain_id}/${item.seq}`}>
                        {item.chain_name} · 第 {item.seq + 1} 跳 ·{' '}
                        {item.redeploy ? '需要重新部署' : '仅同步编排'}
                      </li>
                    ))}
                  </ul>
                  <Button autoFocus disabled={saving} onClick={() => void confirmSave()}>
                    {saving ? '保存中…' : '确认影响并保存'}
                  </Button>
                  <Button variant="outline" disabled={saving} onClick={() => setPending(null)}>
                    返回编辑
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
        ),
        onApply: async (state) => {
          if (pending || saving) return
          if (!name.trim()) throw new Error('请填写模板名称')
          setError('')
          const graph = graphFromEditorState(state)
          const body: OrchestrationTemplateSaveRequest = {
            ...graph,
            nodes: graph.nodes.map((node) =>
              node.kind === 'server' ? { ...node, address: '' } : node,
            ),
            name: name.trim(),
            ...(!saveAsNew && template ? { id: template.id, revision: template.revision } : {}),
          }
          if (body.id) {
            const preview = await api.previewOrchestrationTemplate(body)
            setPending({ body, impacts: preview.affected_chains })
          } else onSaved(await api.saveOrchestrationTemplate(body))
        },
      }}
    />
  )
}
