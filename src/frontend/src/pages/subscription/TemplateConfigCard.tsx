import { useEffect, useMemo, useRef, useState } from 'react'
import { CircleAlertIcon, RefreshCwIcon, RotateCcwIcon, SlidersIcon } from 'lucide-react'

import { requester } from '@/lib/requester'
import type { PublicationJob } from './publication-jobs'
import {
  PRESET_OPTIONS,
  categoriesForPreset,
  effectiveSummary,
  formValuesOf,
  type FormValues,
  type PresetId,
  type SubTemplateConfig,
  type SubTemplateOption,
} from './template-config-form'

const KIND_LABELS: Record<SubTemplateOption['kind'], string> = {
  portable: '主策略',
  acl4ssr: '主策略',
  mihomo: 'Mihomo',
  singbox: 'Sing-box',
  quanx: 'Quantumult X',
}

/**
 * TemplateConfigCard 是订阅页的用户级模板配置卡（四级优先级：强制级 > 用户级 > 面板级 > 默认级）。
 * 管理员强制指派期间显示锁定提示。
 */
export function TemplateConfigCard({
  token,
  settledJobs,
  onSubmitted,
}: {
  token: string
  settledJobs: PublicationJob[]
  onSubmitted: (job: PublicationJob) => void
}) {
  const path = `/api/sub/${token}/template-config`
  const [config, setConfig] = useState<SubTemplateConfig | null>(null)
  const [loadFailed, setLoadFailed] = useState(false)
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [draft, setDraft] = useState<FormValues | null>(null)
  const [submitting, setSubmitting] = useState<'save' | 'reset' | null>(null)
  const [error, setError] = useState('')
  const latestSubmission = useRef<{ id: string; draft: FormValues | null } | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    const submission = latestSubmission.current
    requester
      .getJSON<SubTemplateConfig>(path, {
        signal: controller.signal,
        display: 'silent',
      })
      .then((next) => {
        if (controller.signal.aborted) return
        setConfig(next)
        setLoadFailed(false)
        if (
          submission &&
          latestSubmission.current === submission &&
          settledJobs.some((job) => job.id === submission.id && job.status === 'completed')
        ) {
          setDraft((previous) => (previous === submission.draft ? null : previous))
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setLoadFailed(true)
      })
    return () => controller.abort()
  }, [loadAttempt, path, settledJobs])

  const portable = useMemo(
    () =>
      (config?.templates ?? []).filter(
        (template) =>
          (template.kind === 'portable' || template.kind === 'acl4ssr') && template.id !== '',
      ),
    [config],
  )
  const native = (kind: SubTemplateOption['kind']) =>
    (config?.templates ?? []).filter((template) => template.kind === kind)

  const loadError = loadFailed ? (
    <div className="sub-cfg-actions">
      <p className="sub-cfg-error" role="alert">
        无法读取模板配置。
      </p>
      <button
        className="cg-button"
        type="button"
        onClick={() => setLoadAttempt((attempt) => attempt + 1)}
      >
        重试
      </button>
    </div>
  ) : null
  if (!config) {
    return (
      <div className="sub-mod-body">
        {loadError ?? (
          <p className="sub-cfg-hint" role="status">
            正在读取模板配置…
          </p>
        )}
      </div>
    )
  }

  const locked = config.forced_portable
  const form = draft ?? formValuesOf(config)
  const { mode, preset, categories, portableId, mihomoId, singboxId, quanxId, regionGroups } = form
  const updateForm = (changes: Partial<FormValues>) => {
    setDraft((previous) => ({ ...(previous ?? form), ...changes }))
  }
  const submit = async (action: 'save' | 'reset') => {
    if (submitting) return
    setSubmitting(action)
    setError('')
    const reset = action === 'reset'
    try {
      const job = await requester.postJSON<PublicationJob>(
        reset ? `${path}/reset` : path,
        reset
          ? {}
          : {
              mode,
              preset,
              categories,
              portable_template_id: portableId,
              mihomo_template_id: mihomoId,
              singbox_template_id: singboxId,
              quanx_template_id: quanxId,
              region_groups_enabled: regionGroups,
            },
        { display: 'silent', headers: { Prefer: 'respond-async' } },
      )
      latestSubmission.current = { id: job.id, draft }
      onSubmitted(job)
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : reset
            ? '恢复默认失败，请稍后重试。'
            : '保存失败，请稍后重试。',
      )
    } finally {
      setSubmitting(null)
    }
  }

  return (
    <div className="sub-mod-body sub-cfg-body">
      {loadError}
      <p className="sub-cfg-hint">
        当前生效：{effectiveSummary(config.effective, config.templates)}
      </p>
      {locked ? (
        <div className="sub-cfg-lock" role="note">
          <CircleAlertIcon size={14} aria-hidden="true" />
          <span>
            {config.forced_summary}，你的自选配置暂时不生效。如需调整请联系管理员解除强制。
          </span>
        </div>
      ) : (
        <>
          {config.panel_assigned_summary && (
            <p className="sub-cfg-hint">{config.panel_assigned_summary}。</p>
          )}
          {config.default_summary && !config.panel_assigned_summary && (
            <p className="sub-cfg-hint">
              {config.default_summary}，你可以在下方设置自己的模板配置。
            </p>
          )}
          {config.user_configured ? (
            <p className="sub-cfg-hint">正在使用你自己的模板配置（用户级，优先于管理员默认）。</p>
          ) : null}
          <div className="sub-cfg-row">
            <label className="sub-field-label" htmlFor="sub-cfg-mode">
              规则来源
            </label>
            <div className="sub-cfg-seg" role="group" aria-label="规则来源">
              <button
                type="button"
                aria-pressed={mode === 'suggested'}
                onClick={() => {
                  updateForm({
                    mode: 'suggested',
                    categories: categoriesForPreset(preset, config.categories),
                  })
                }}
              >
                建议规则
              </button>
              <button
                type="button"
                aria-pressed={mode === 'template'}
                onClick={() => updateForm({ mode: 'template' })}
              >
                自定义模板
              </button>
            </div>
          </div>

          {mode === 'suggested' ? (
            <>
              <div className="sub-cfg-row">
                <label className="sub-field-label" htmlFor="sub-cfg-preset">
                  规则预设
                </label>
                <select
                  id="sub-cfg-preset"
                  className="sub-select"
                  value={preset}
                  onChange={(event) => {
                    const next = event.target.value as PresetId
                    updateForm({
                      preset: next,
                      categories: categoriesForPreset(next, config.categories),
                    })
                  }}
                >
                  {PRESET_OPTIONS.map(([id, label]) => (
                    <option key={id} value={id}>
                      {label}
                    </option>
                  ))}
                </select>
              </div>
              <fieldset className="sub-cfg-fieldset">
                <legend className="sub-field-label">生效分组</legend>
                <div className="sub-cfg-cats">
                  {config.categories.map((category) => (
                    <label key={category.id} className="sub-cfg-cat">
                      <input
                        type="checkbox"
                        checked={categories.includes(category.id)}
                        onChange={(event) =>
                          updateForm({
                            categories: event.target.checked
                              ? [...categories, category.id]
                              : categories.filter((id) => id !== category.id),
                          })
                        }
                      />
                      <span aria-hidden="true">{category.icon}</span>
                      <span>{category.label}</span>
                    </label>
                  ))}
                </div>
              </fieldset>
            </>
          ) : (
            <div className="sub-cfg-row">
              <label className="sub-field-label" htmlFor="sub-cfg-portable">
                中立 / ACL4SSR 模板
              </label>
              <select
                id="sub-cfg-portable"
                className="sub-select"
                value={portableId}
                onChange={(event) => updateForm({ portableId: event.target.value })}
              >
                <option value="">选择模板</option>
                {portable.map((template) => (
                  <option key={template.id} value={template.id}>
                    {template.name}
                  </option>
                ))}
              </select>
            </div>
          )}

          <details className="sub-cfg-native">
            <summary>客户端原生模板</summary>
            <div className="sub-cfg-native-grid">
              {(
                [
                  [
                    'mihomo',
                    mihomoId,
                    'mihomoId',
                    config.forced_mihomo,
                    config.assigned_mihomo_template_id,
                  ],
                  [
                    'singbox',
                    singboxId,
                    'singboxId',
                    config.forced_singbox,
                    config.assigned_singbox_template_id,
                  ],
                  [
                    'quanx',
                    quanxId,
                    'quanxId',
                    config.forced_quanx,
                    config.assigned_quanx_template_id,
                  ],
                ] as const
              ).map(([kind, value, field, isForced, assignedId]) => {
                const assigned = assignedId
                  ? config.templates.find((template) => template.id === assignedId)?.name
                  : ''
                return (
                  <div key={kind} className="sub-cfg-native-cell">
                    <label className="sub-field-label" htmlFor={`sub-cfg-${kind}`}>
                      {KIND_LABELS[kind]}
                    </label>
                    <select
                      id={`sub-cfg-${kind}`}
                      className="sub-select"
                      value={value}
                      disabled={isForced}
                      onChange={(event) => updateForm({ [field]: event.target.value })}
                    >
                      <option value="">{isForced ? '跟随强制指派' : '跟随主策略'}</option>
                      {native(kind).map((template) => (
                        <option key={template.id} value={template.id}>
                          {template.name}
                        </option>
                      ))}
                    </select>
                    {assigned && !isForced ? (
                      <p className="sub-cfg-hint">管理员默认「{assigned}」，你的选择优先</p>
                    ) : null}
                  </div>
                )
              })}
            </div>
          </details>

          <div className="space-y-1">
            <label className="sub-cfg-cat">
              <input
                type="checkbox"
                checked={regionGroups}
                onChange={(event) => updateForm({ regionGroups: event.target.checked })}
                aria-describedby="sub-cfg-region-groups-hint"
              />
              <span>开启地区分组</span>
            </label>
            <p id="sub-cfg-region-groups-hint" className="sub-cfg-hint">
              按节点地区自动添加分组（含无地区分组）；关闭后保留节点和来源分组。
            </p>
          </div>

          {error && (
            <p className="sub-cfg-error" role="alert">
              {error}
            </p>
          )}
          <div className="sub-cfg-actions">
            <button
              type="button"
              className="cg-button is-primary"
              disabled={
                submitting !== null ||
                (mode === 'template' && !portableId) ||
                (mode === 'suggested' && categories.length === 0)
              }
              onClick={() => submit('save')}
            >
              {submitting === 'save' ? (
                <RefreshCwIcon aria-hidden="true" />
              ) : (
                <SlidersIcon aria-hidden="true" />
              )}
              {submitting === 'save' ? '提交中…' : '保存'}
            </button>
            <button
              type="button"
              className="cg-button"
              disabled={submitting !== null}
              onClick={() => submit('reset')}
              title={
                config.user_configured
                  ? '清除我在订阅页保存的模板配置，回落到管理员默认'
                  : '把表单重置为当前生效配置'
              }
            >
              {submitting === 'reset' ? (
                <RefreshCwIcon aria-hidden="true" />
              ) : (
                <RotateCcwIcon aria-hidden="true" />
              )}
              {submitting === 'reset' ? '提交中…' : '恢复默认（清除我的配置）'}
            </button>
            <span className="sub-cfg-note">发布完成后，客户端下次刷新订阅时生效。</span>
          </div>
        </>
      )}
    </div>
  )
}
