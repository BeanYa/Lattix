import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { ArrowLeftIcon, Trash2Icon, TriangleAlertIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { api } from '@/lib/api'
import { useAppDialog } from '@/lib/app-dialog'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { cn } from '@/lib/utils'
import type {
  CatalogProbeRow,
  CatalogProbeStats,
  Chain,
  OrchestrationTemplate,
  Server,
  XrayNode,
} from '@/lib/types'

import { DraftOrchestrationEditor } from '../orchestration/DraftOrchestrationEditor'
import { draftOrchestrationDTO } from '../orchestration/orchestration-draft'
import { parseChainEdit, useChainForm } from '../use-chain-form'
import { canvasPathHops, prefillCanvas } from './chain-canvas'
import { CanvasServerPanel } from './CanvasServerPanel'
import { ChainCanvas } from './ChainCanvas'
import { ChainDetailsStep } from './ChainDetailsStep'
import { ChainValidationSummary } from './ChainResultPreview'
import { useChainCanvas } from './use-chain-canvas'

// 结果预览复用链路详情（cg-chain-entry）的列表样式。
import '../../chains.css'
import './chain-wizard.css'

/**
 * 链路新增/编辑两步向导（/network/chains/new、/network/chains/:id/edit）：
 * 第一步画布拓扑（左侧服务器列表 + 自由画布，校验通过才可进入下一步），
 * 第二步协议与细节表单。提交契约与原对话框完全一致。
 */
export function ChainWizardPage() {
  const [location, navigate] = useLocation()
  const editMatch = location.match(/^\/network\/chains\/(\d+)\/edit$/)
  const editingId = editMatch ? Number(editMatch[1]) : null

  const { showOperation } = useOperationProgress()
  const { confirm } = useAppDialog()
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
  const [templates, setTemplates] = useState<OrchestrationTemplate[]>([])
  const [templateError, setTemplateError] = useState('')
  const [servers, setServers] = useState<Server[]>([])
  const [panelShort, setPanelShort] = useState('')
  const [probes, setProbes] = useState<CatalogProbeStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [bootError, setBootError] = useState('')
  const [step, setStep] = useState<1 | 2>(1)
  const bootedRef = useRef(false)

  useEffect(() => {
    let active = true
    Promise.all([api.chains(), api.nodes(), api.servers()])
      .then(([nextChains, nextNodes, nextServers]) => {
        if (!active) return
        setChains(nextChains)
        setNodes(nextNodes)
        setServers(nextServers)
        setLoading(false)
      })
      .catch((err) => {
        if (!active) return
        setLoadError(String(err))
        setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  useEffect(() => {
    api
      .settings()
      .then((s) => setPanelShort(s.panel_short))
      .catch(() => {})
  }, [])

  // 三网测速统计仅作节点卡展示：60s 拉取与服务器页一致（探测 5 分钟一轮）。
  useEffect(() => {
    let active = true
    const loadProbes = () => {
      api
        .catalogProbes()
        .then((result) => {
          if (active) setProbes(result)
        })
        .catch(() => {
          if (active) setProbes(null)
        })
    }
    loadProbes()
    const timer = setInterval(loadProbes, 60000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [])

  // 已启用自动编排的跳服务器（编辑模式）：删除画布节点前提示编排配置一并删除。
  const orchestratedServerIds = useMemo(() => {
    const ids = new Set<number>()
    if (!editingId) return ids
    const chain = chains.find((candidate) => candidate.id === editingId)
    for (const hop of chain?.hops ?? []) {
      if (hop.orchestration && hop.orchestration.nodes.length > 0) ids.add(hop.server_id)
    }
    return ids
  }, [chains, editingId])
  const confirmRemoveOrchestration = useCallback(
    async (alias: string) =>
      confirm({
        title: '删除已启用自动编排的节点',
        description: `「${alias}」已启用自动编排，删除该节点会一并删除它的编排配置。`,
        confirmLabel: '删除并移除编排',
      }),
    [confirm],
  )
  const refreshTemplates = useCallback(() => {
    api
      .orchestrationTemplates()
      .then((value) => {
        setTemplates(value)
        setTemplateError('')
      })
      .catch((error) => setTemplateError(String(error)))
  }, [])
  useEffect(() => {
    refreshTemplates()
  }, [refreshTemplates])
  const editChain = useMemo(
    () => (editingId ? (chains.find((chain) => chain.id === editingId) ?? null) : null),
    [chains, editingId],
  )
  const [orchestrationNodeId, setOrchestrationNodeId] = useState<string | null>(null)
  const confirmReplaceOrchestration = useCallback(
    async (alias: string) =>
      confirm({
        title: '替换该跳编排',
        description: `「${alias}」已在画布中。应用模板将替换该跳的编排图；已明确设置的实例参数将保留。`,
        confirmLabel: '替换编排',
      }),
    [confirm],
  )
  const canvas = useChainCanvas({
    servers,
    templates,
    orchestratedServerIds,
    confirmRemoveOrchestration,
    confirmReplaceOrchestration,
  })
  const orchestrationFor = useCallback(
    (serverId: number) => {
      const node = canvas.nodes.find((n) => n.serverId === serverId)
      return node?.orchestration
        ? draftOrchestrationDTO(
            node.orchestration,
            servers,
            node.serverId,
            templates.find((t) => t.id === node.orchestration?.template_id),
          )
        : undefined
    },
    [canvas.nodes, servers, templates],
  )
  const editOrchestration = useCallback(
    (serverId: number) => {
      setOrchestrationNodeId(canvas.nodes.find((n) => n.serverId === serverId)?.id ?? null)
    },
    [canvas.nodes],
  )
  const orchestrationNode = canvas.nodes.find((node) => node.id === orchestrationNodeId)
  const editorPath = orchestrationNode
    ? (canvas.paths.find((path) => path.some((node) => node.id === orchestrationNode.id)) ?? [
        orchestrationNode,
      ])
    : []
  const editorHops = canvasPathHops(editorPath, servers, templates)
  const editorHop = editorHops.find((hop) => hop.server_id === orchestrationNode?.serverId)
  const editorChain: Chain = {
    ...(editChain ?? {
      id: 0,
      name: '未保存链路',
      status: 'pending',
      error: '',
      created_at: '',
      service_node_id: 0,
      endpoint_id: 0,
      entry_port: 0,
      traffic_multiplier: '1.000',
      published_revision_id: 0,
      desired_revision_id: 0,
      revision_forced: false,
      revision_tasks: [],
    }),
    hops: editorHops,
    service_external: servers.find((s) => s.id === editorPath.at(-1)?.serverId)?.external_exit,
  }
  const chainForm = useChainForm({
    chains,
    servers,
    topology: canvas.formTopology,
    paths: canvas.validation.paths,
    templates,
    onError: setBootError,
    onSaved: () => navigate('/network/chains'),
    showOperation,
  })

  // 编辑模式：链列表与节点就绪后回填一次（画布节点 + 协议细节表单）。
  useEffect(() => {
    if (bootedRef.current || loading) return
    if (!editingId) {
      bootedRef.current = true
      return
    }
    const chain = chains.find((candidate) => candidate.id === editingId)
    if (!chain) {
      if (chains.length > 0 || loadError) {
        setBootError('链路不存在或已被删除')
        bootedRef.current = true
      }
      return
    }
    const parsed = parseChainEdit(chain, nodes)
    if (!parsed.ok) {
      setBootError(parsed.error)
      bootedRef.current = true
      return
    }
    const prefill = prefillCanvas(chain, parsed.value.exitPort)
    canvas.prefill(prefill.nodes, prefill.edges)
    chainForm.beginEdit(chain, parsed.value)
    bootedRef.current = true
  }, [loading, chains, nodes, editingId, loadError, canvas, chainForm])

  const probeTitle = probes ? `${probes.province}${probes.city ? ` · ${probes.city}` : ''}` : null
  // 编辑时连线用该链的身份色；新建用默认链路色。
  const linkColor = editingId ? `var(--panel-line-${editingId % 9})` : 'var(--panel-line-2)'

  const issues = useMemo(() => canvas.validation.issues, [canvas.validation])
  // 编辑模式仍只维护一条既有链路：多路径画布不允许继续（请新建链路）。
  const editMultiPath = editingId !== null && canvas.validation.paths.length > 1
  const canProceed = canvas.validation.valid && !editMultiPath
  // 三网测速按服务器 ID 索引：节点卡与左侧服务器列表卡共用
  const probesByServer = useMemo(() => {
    const map = new Map<number, CatalogProbeRow[]>()
    if (probes) {
      for (const [id, entry] of Object.entries(probes.servers)) {
        map.set(Number(id), entry.rows)
      }
    }
    return map
  }, [probes])

  if (loading) {
    return (
      <Page>
        <LoadingState>加载链路数据…</LoadingState>
      </Page>
    )
  }
  if (loadError && chains.length === 0) {
    return (
      <Page>
        <Notice tone="danger" title="链路数据加载失败">
          {loadError}
        </Notice>
      </Page>
    )
  }

  return (
    <Page>
      <PageHeader
        title={editingId ? '编辑链路' : '创建链路'}
        description={
          step === 1
            ? '第一步：从左侧拖入服务器组建拓扑（可分支/汇合）；每条从入网流量通往 Internet 的路径都会创建一条链路。'
            : editingId
              ? '第二步：配置协议与细节；结果预览展示编辑后生效的链路。'
              : `第二步：配置协议与细节；将按结果预览顺序创建 ${chainForm.chainCount} 条链路。`
        }
        actions={
          <>
            <Button variant="ghost" onClick={() => navigate('/network/chains')}>
              <ArrowLeftIcon />
              返回列表
            </Button>
            <div className="cw-steps" aria-label="向导步骤">
              <span className={cn('cw-step', step === 1 && 'is-active')}>
                <span className="cw-step-index">1</span>链路拓扑
              </span>
              <span className={cn('cw-step', step === 2 && 'is-active', step === 2 && 'is-done')}>
                <span className="cw-step-index">2</span>协议与细节
              </span>
            </div>
            {step === 1 ? (
              <Button
                onClick={() => setStep(2)}
                disabled={!canProceed}
                title={
                  canProceed
                    ? undefined
                    : editMultiPath
                      ? '编辑模式仅支持单条链路，请断开多余分支或新建链路'
                      : '链路拓扑未通过校验，请先解决下方问题'
                }
              >
                下一步：协议与细节
              </Button>
            ) : null}
          </>
        }
      />
      {bootError ? (
        <Notice
          tone="danger"
          title="无法进入编辑"
          actions={
            <Button variant="outline" size="sm" onClick={() => navigate('/network/chains')}>
              返回列表
            </Button>
          }
        >
          {bootError}
        </Notice>
      ) : step === 1 ? (
        <div className="cw-layout">
          <CanvasServerPanel
            servers={servers}
            templates={templates}
            templateError={templateError}
            onAddTemplate={(id) => {
              void canvas.addTemplate(id)
            }}
            emptyCanvas={canvas.nodes.length === 0}
            probesByServer={probesByServer}
            probeTitle={probeTitle}
            usedCount={canvas.usedCount}
            canAddMore={canvas.canAddMore}
            onAdd={(server) => canvas.addVisibleNode(server.id)}
          />
          <div className="cw-canvas-col">
            <div className="cw-canvas-toolbar">
              <span>
                节点 <span className="cw-canvas-toolbar-count">{canvas.nodes.length}</span>
              </span>
              <span>
                拖动空白平移 · 点击出口再点入口连接（可多路分支/汇合）· 点击连线断开 · Esc 取消
              </span>
              <Button
                variant="ghost"
                size="xs"
                onClick={() => canvas.reset()}
                disabled={canvas.nodes.length === 0}
              >
                <Trash2Icon />
                清空画布
              </Button>
            </div>
            <ChainCanvas
              controller={canvas}
              probesByServer={probesByServer}
              probeTitle={probeTitle}
              linkColor={linkColor}
              orchestrationFor={orchestrationFor}
              onEditOrchestration={editOrchestration}
            />
            <ChainValidationSummary
              pathCount={canvas.validation.paths.length}
              warnings={canvas.validation.warnings}
              serverById={canvas.serverById}
            />
            <div className="cw-issues" role="status">
              {editMultiPath ? (
                <span className="cw-issue">
                  <TriangleAlertIcon aria-hidden />
                  编辑模式仅支持单条链路：当前画布会生成 {canvas.validation.paths.length}{' '}
                  条，请断开多余分支或新建链路
                </span>
              ) : null}
              {issues.map((issue, index) => (
                <span
                  key={`${issue.code}-${issue.nodeId ?? 'canvas'}-${index}`}
                  className={cn('cw-issue', canvas.nodes.length === 0 && 'is-hint')}
                >
                  <TriangleAlertIcon aria-hidden />
                  {issue.message}
                </span>
              ))}
            </div>
          </div>
        </div>
      ) : (
        <ChainDetailsStep
          controller={chainForm}
          panelShort={panelShort}
          chainType={canvas.formTopology.chainType}
          entryPort={canvas.formTopology.entryPort}
          onBack={() => setStep(1)}
          warnings={canvas.validation.warnings}
          serverById={canvas.serverById}
        />
      )}
      {orchestrationNode && editorHop ? (
        <DraftOrchestrationEditor
          key={orchestrationNode.id}
          chain={editorChain}
          hop={editorHop}
          value={orchestrationNode.orchestration}
          templates={templates}
          servers={servers}
          probesByServer={probesByServer}
          probeTitle={probeTitle}
          onTemplatesChanged={refreshTemplates}
          onClose={() => setOrchestrationNodeId(null)}
          onApply={(draft) => {
            const error = canvas.applyOrchestration(orchestrationNode.id, draft)
            // The shared editor catches apply errors and displays them inside the dialog.
            if (error) throw new Error(error)
            setOrchestrationNodeId(null)
          }}
        />
      ) : null}
    </Page>
  )
}
