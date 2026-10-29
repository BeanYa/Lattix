import { describe, expect, it } from 'vitest'

import {
  categoriesForPreset,
  effectiveSummary,
  formValuesOf,
  presetOfCategories,
  type SubTemplateConfig,
} from './template-config-form'

const categories = [
  { id: 'ads', label: '广告拦截', icon: '🛑', in_minimal: false, in_balanced: false },
  { id: 'ai', label: 'AI 服务', icon: '💬', in_minimal: false, in_balanced: true },
  { id: 'private', label: '私有网络', icon: '🏠', in_minimal: true, in_balanced: true },
  { id: 'domestic', label: '国内服务', icon: '🔒', in_minimal: true, in_balanced: true },
  { id: 'overseas', label: '非中国', icon: '🌐', in_minimal: true, in_balanced: true },
]

function config(patch: Partial<SubTemplateConfig>): SubTemplateConfig {
  return {
    user_configured: false,
    effective_source: 'default',
    region_groups_enabled: true,
    forced_portable: false,
    forced_mihomo: false,
    forced_singbox: false,
    forced_quanx: false,
    user: {
      mode: 'suggested',
      preset: 'balanced',
      categories: ['ai', 'private', 'domestic', 'overseas'],
      portable_template_id: '',
      mihomo_template_id: '',
      singbox_template_id: '',
      quanx_template_id: '',
    },
    effective: {
      mode: 'suggested',
      preset: 'balanced',
      categories: ['ai', 'private', 'domestic', 'overseas'],
      portable_template_id: '',
      mihomo_template_id: '',
      singbox_template_id: '',
      quanx_template_id: '',
    },
    templates: [],
    categories,
    ...patch,
  }
}

describe('formValuesOf', () => {
  it('uses the effective config when the user has no user-level config', () => {
    const values = formValuesOf(
      config({
        user_configured: false,
        user: { ...config({}).user, categories: ['ai'] },
        effective: { ...config({}).effective, categories: ['private', 'domestic'] },
      }),
    )
    expect(values.categories).toEqual(['private', 'domestic'])
  })

  it('uses the user-level config once the user saved one', () => {
    const values = formValuesOf(
      config({
        user_configured: true,
        user: { ...config({}).user, mode: 'suggested', categories: ['ai'] },
        effective: { ...config({}).effective, categories: ['private'] },
      }),
    )
    expect(values.categories).toEqual(['ai'])
  })

  it('follows the effective template slot when the user follows the main strategy', () => {
    const values = formValuesOf(
      config({
        user_configured: false,
        effective: {
          ...config({}).effective,
          mode: 'template',
          portable_template_id: 'tpl-default',
        },
      }),
    )
    expect(values.mode).toBe('template')
    expect(values.portableId).toBe('tpl-default')
  })

  it('takes the region-groups switch from the effective value returned by the server', () => {
    expect(formValuesOf(config({ region_groups_enabled: false })).regionGroups).toBe(false)
    expect(formValuesOf(config({ region_groups_enabled: true })).regionGroups).toBe(true)
  })

  it('keeps legacy responses without the switch enabled', () => {
    const legacy = { ...config({}) } as Partial<SubTemplateConfig>
    delete legacy.region_groups_enabled
    expect(formValuesOf(legacy as SubTemplateConfig).regionGroups).toBe(true)
  })

  it('keeps client-native template slots on the user-level value (empty = follow main strategy)', () => {
    const values = formValuesOf(
      config({
        user_configured: false,
        user: {
          ...config({}).user,
          mihomo_template_id: '',
          singbox_template_id: '',
          quanx_template_id: '',
        },
        effective: {
          ...config({}).effective,
          mihomo_template_id: 'tpl-admin-mihomo',
          singbox_template_id: 'tpl-admin-singbox',
          quanx_template_id: 'tpl-admin-quanx',
        },
      }),
    )
    expect(values.mihomoId).toBe('')
    expect(values.singboxId).toBe('')
    expect(values.quanxId).toBe('')
  })

  it('shows the user-level client-native template once the user picked one', () => {
    const values = formValuesOf(
      config({
        user_configured: true,
        user: { ...config({}).user, mihomo_template_id: 'tpl-user-mihomo' },
        effective: { ...config({}).effective, mihomo_template_id: 'tpl-user-mihomo' },
      }),
    )
    expect(values.mihomoId).toBe('tpl-user-mihomo')
  })
})

describe('preset helpers', () => {
  it('resolves the preset matching the checked categories', () => {
    expect(presetOfCategories(['private', 'domestic', 'overseas'], categories)).toBe('minimal')
    expect(presetOfCategories(['ai', 'private', 'domestic', 'overseas'], categories)).toBe(
      'balanced',
    )
  })

  it('falls back to balanced for category sets no preset describes', () => {
    expect(presetOfCategories(['ai', 'private'], categories)).toBe('balanced')
  })

  it('derives the checked categories of a preset', () => {
    expect(categoriesForPreset('minimal', categories)).toEqual(['private', 'domestic', 'overseas'])
    expect(categoriesForPreset('comprehensive', categories)).toEqual(categories.map((c) => c.id))
  })
})

describe('effectiveSummary', () => {
  it('summarizes suggested categories and templates', () => {
    expect(effectiveSummary(config({}).effective, [])).toBe('建议规则 · 4 个分组')
    expect(
      effectiveSummary(
        {
          ...config({}).effective,
          mode: 'template',
          portable_template_id: 'tpl-default',
        },
        [{ id: 'tpl-default', name: '默认模板', kind: 'portable' }],
      ),
    ).toBe('模板「默认模板」')
  })
})
