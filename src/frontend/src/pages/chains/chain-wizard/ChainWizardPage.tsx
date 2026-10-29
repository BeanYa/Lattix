import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { ArrowLeftIcon, Trash2Icon, TriangleAlertIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { LoadingState, Notice, Page, PageHeader } from '@/components/PagePrimitives'
import { api } from '@/lib/api'
import { useOperationProgress } from '@/lib/operation-progress-context'
import { cn } from '@/lib/utils'
import type { CatalogProbeRow, CatalogProbeStats, Chain, Server, XrayNode } from '@/lib/types'

import { parseChainEdit, useChainForm } from '../use-chain-form'
import { prefillCanvas } from './chain-canvas'
import { CanvasServerPanel } from './CanvasServerPanel'
import { ChainCanvas } from './ChainCanvas'
import { ChainDetailsStep } from './ChainDetailsStep'
import { useChainCanvas } from './use-chain-canvas'

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
  const [chains, setChains] = useState<Chain[]>([])
  const [nodes, setNodes] = useState<XrayNode[]>([])
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

  const canvas = useChainCanvas({ servers })
  const chainForm = useChainForm({
    chains,
    servers,
    topology: canvas.formTopology,
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
            ? '第一步：从左侧拖入服务器组建链路拓扑，从入网流量节点进入，末端连到 Internet 出口。'
            : '第二步：配置出口协议与细节，保存后按出口到入口依次部署。'
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
                disabled={!canvas.validation.valid}
                title={canvas.validation.valid ? undefined : '链路拓扑未通过校验，请先解决下方问题'}
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
                节点 <span className="cw-canvas-toolbar-count">{canvas.nodes.length}</span> / 4
              </span>
              <span>拖动空白平移 · 点击出口再点入口连接 · 点击连线断开 · Esc 取消</span>
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
            />
            <div className="cw-issues" role="status">
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
          exitPort={canvas.formTopology.port}
          onBack={() => setStep(1)}
        />
      )}
    </Page>
  )
}
