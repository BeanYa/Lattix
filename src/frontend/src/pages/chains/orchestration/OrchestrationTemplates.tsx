import { useCallback, useEffect, useRef, useState } from 'react'
import type {
  CatalogProbeRow,
  OrchestrationTemplate,
  OrchestrationTemplateImpact,
  Server,
} from '@/lib/types'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { OrchestrationTemplateEditor } from './OrchestrationTemplateEditor'
import './orchestration.css'

export function OrchestrationTemplates({
  servers: serversProp,
  probesByServer,
  probeTitle,
}: {
  servers?: Server[]
  probesByServer?: Map<number, CatalogProbeRow[]>
  probeTitle?: string | null
} = {}) {
  const [templates, setTemplates] = useState<OrchestrationTemplate[]>([])
  const [fetchedServers, setFetchedServers] = useState<Server[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editor, setEditor] = useState<{ template?: OrchestrationTemplate } | null>(null)
  const [deleting, setDeleting] = useState<{
    template: OrchestrationTemplate
    impacts: OrchestrationTemplateImpact[]
  } | null>(null)
  const [busy, setBusy] = useState(false)
  const request = useRef(0)
  const load = useCallback(
    async (signal?: AbortSignal) => {
      const current = ++request.current
      setLoading(true)
      setError('')
      try {
        const [list, servers] = await Promise.all([
          api.orchestrationTemplates({ signal }),
          serversProp ? Promise.resolve(serversProp) : api.servers({ signal }),
        ])
        if (signal?.aborted || current !== request.current) return
        setTemplates(list)
        setFetchedServers(servers)
      } catch (err) {
        if (!signal?.aborted && current === request.current)
          setError(err instanceof Error ? err.message : '模板加载失败')
      } finally {
        if (!signal?.aborted && current === request.current) setLoading(false)
      }
    },
    [serversProp],
  )
  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])
  const servers = serversProp ?? fetchedServers
  const previewDelete = async (template: OrchestrationTemplate) => {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      const preview = await api.previewOrchestrationTemplate({
        id: template.id,
        revision: template.revision,
        name: template.name,
        nodes: template.nodes,
        edges: template.edges,
      })
      setDeleting({ template, impacts: preview.affected_chains })
    } catch (err) {
      setError(err instanceof Error ? err.message : '无法获取关联链路，请刷新后重试')
    } finally {
      setBusy(false)
    }
  }
  const confirmDelete = async () => {
    if (!deleting || busy) return
    setBusy(true)
    setError('')
    try {
      await api.deleteOrchestrationTemplate(deleting.template.id)
      setDeleting(null)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败，请刷新后重试')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="orch-template-library" aria-label="节点编排模板">
      <div className="orch-library-head">
        <div>
          <h2>节点编排模板</h2>
          <p>共享服务器与触发动作；地址在链路实例中填写。</p>
        </div>
        <Button disabled={loading} onClick={() => setEditor({})}>
          新建模板
        </Button>
      </div>
      {error ? (
        <p className="orch-library-error" role="alert">
          {error}{' '}
          <Button size="sm" variant="outline" onClick={() => void load()}>
            刷新模板
          </Button>
        </p>
      ) : null}
      {loading ? (
        <p role="status">正在加载模板…</p>
      ) : templates.length === 0 ? (
        <p className="orch-empty">还没有编排模板，创建后可在链路中复用。</p>
      ) : (
        <div className="orch-template-list">
          {templates.map((template) => {
            const rootId = template.nodes.find(
              (node) => node.kind === 'server' && node.is_root,
            )?.server_id
            return (
              <article className="orch-template-item" key={template.id}>
                <div>
                  <h3>{template.name}</h3>
                  <p>
                    主选：{servers.find((server) => server.id === rootId)?.alias ?? `#${rootId}`} ·{' '}
                    {template.nodes.filter((node) => node.kind === 'server').length} 台服务器 ·{' '}
                    {template.nodes.filter((node) => node.kind === 'trigger').length} 个触发器 ·{' '}
                    {template.usage_count} 个关联跳位
                  </p>
                </div>
                <div className="orch-library-actions">
                  <Button size="sm" variant="outline" onClick={() => setEditor({ template })}>
                    编辑共享模板
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => setEditor({ template })}
                    aria-label={`重命名模板 ${template.name}`}
                  >
                    重命名
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      setEditor({
                        template: {
                          ...structuredClone(template),
                          id: 0,
                          revision: 0,
                          name: `${template.name} 副本`,
                          usage_count: 0,
                        },
                      })
                    }
                  >
                    另存为新模板
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() => void previewDelete(template)}
                    aria-label={`删除模板 ${template.name}`}
                  >
                    删除
                  </Button>
                </div>
              </article>
            )
          })}
        </div>
      )}
      {deleting ? (
        <div className="orch-library-confirm" role="alertdialog" aria-label="确认删除模板">
          <h3>删除「{deleting.template.name}」？</h3>
          <p>
            以下 {deleting.impacts.length}{' '}
            个关联跳位将先分离为独立副本，保留当前完整编排与实例参数：
          </p>
          <ul>
            {deleting.impacts.map((item) => (
              <li key={`${item.chain_id}/${item.seq}`}>
                {item.chain_name} · 第 {item.seq + 1} 跳
              </li>
            ))}
          </ul>
          <Button disabled={busy} onClick={() => void confirmDelete()}>
            {busy ? '删除中…' : '确认分离并删除'}
          </Button>
          <Button variant="outline" disabled={busy} onClick={() => setDeleting(null)}>
            取消
          </Button>
        </div>
      ) : null}
      {editor ? (
        <OrchestrationTemplateEditor
          template={editor.template}
          servers={servers}
          probesByServer={probesByServer}
          probeTitle={probeTitle}
          onClose={() => setEditor(null)}
          onSaved={() => {
            setEditor(null)
            void load()
          }}
        />
      ) : null}
    </section>
  )
}
