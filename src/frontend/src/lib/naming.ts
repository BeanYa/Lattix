import { countryFlag } from '@/lib/geography'
import type { Server } from '@/lib/types'

export const DEFAULT_DIRECT_NAME_TEMPLATE = '{{COUNTRY_FLAG}}{{LOCATION}}-Direct'
export const DEFAULT_RELAY_NAME_TEMPLATE = '{{EXIT.COUNTRY_FLAG}}-Out'

export interface NameTemplateContext {
  servers: Server[]
  protocol: string
  port?: string
}

export interface NameTemplateResult {
  preview: string
  error: string
}

const tokenPattern = /\{\{\s*([^{}]+?)\s*\}\}/g
const countryNames = new Intl.DisplayNames(['zh-CN'], { type: 'region' })

function serverAttribute(server: Server, attribute: string, key: string): string {
  const values: Record<string, string> = {
    ID: String(server.id),
    NAME: server.alias,
    COUNTRY: countryNames.of(server.country_code) ?? '',
    COUNTRY_CODE: server.country_code,
    COUNTRY_FLAG: countryFlag(server.country_code),
    LOCATION: server.location,
    ADDRESS: server.address,
  }
  if (!(attribute in values)) throw new Error(`参数 {{${key}}} 的属性不存在`)
  if (!values[attribute]) throw new Error(`参数 {{${key}}} 缺少对应的服务器资料`)
  return values[attribute]
}

function serverTag(server: Server, index: number, key: string): string {
  if (!Number.isInteger(index) || index < 0 || index >= server.tags.length) {
    throw new Error(`参数 {{${key}}} 数组越界：当前服务器共 ${server.tags.length} 个标签`)
  }
  return server.tags[index]
}

function resolveToken(key: string, context: NameTemplateContext): string {
  const servers = context.servers
  if (servers.length === 0) throw new Error('请先完成服务器拓扑选择')
  const global = servers[0]
  const globalTag = key.match(/^TAG\[(\d+)\]$/)
  if (globalTag) return serverTag(global, Number(globalTag[1]), key)

  const scoped = key.match(/^(ENTRY|EXIT|HOP\[(\d+)\])\.([A-Z][A-Z0-9_]*)(?:\[(\d+)\])?$/)
  if (scoped) {
    const index =
      scoped[1] === 'ENTRY'
        ? 0
        : scoped[1] === 'EXIT'
          ? servers.length - 1
          : Number(scoped[2])
    if (index < 0 || index >= servers.length) {
      throw new Error(`参数 {{${key}}} 数组越界：当前链路共 ${servers.length} 跳`)
    }
    if (scoped[3] === 'TAG') {
      if (scoped[4] === undefined) throw new Error(`参数 {{${key}}} 缺少标签索引`)
      return serverTag(servers[index], Number(scoped[4]), key)
    }
    if (scoped[4] !== undefined) throw new Error(`参数 {{${key}}} 的属性不支持数组索引`)
    return serverAttribute(servers[index], scoped[3], key)
  }

  if (!/^[A-Z][A-Z0-9_]*$/.test(key)) throw new Error(`名称模板包含无效参数 {{${key}}}`)
  switch (key) {
    case 'PROTOCOL':
      return context.protocol
    case 'PORT':
      return context.port || 'auto'
    case 'HOPS':
      return String(servers.length)
    case 'ENTRY':
      return servers[0].alias
    case 'EXIT':
      return servers[servers.length - 1].alias
    case 'SERVER':
    case 'NAME':
      return global.alias
    case 'SERVER_ID':
    case 'ID':
      return String(global.id)
    case 'COUNTRY':
    case 'COUNTRY_CODE':
    case 'COUNTRY_FLAG':
    case 'LOCATION':
    case 'ADDRESS':
      return serverAttribute(global, key, key)
    default:
      throw new Error(`不支持的名称参数 {{${key}}}`)
  }
}

export function evaluateNameTemplate(
  template: string,
  context: NameTemplateContext,
): NameTemplateResult {
  try {
    if (template.length > 200) throw new Error('名称模板不能超过 200 个字符')
    if ((template.match(/\{\{/g)?.length ?? 0) !== (template.match(/\}\}/g)?.length ?? 0)) {
      return { preview: template, error: '' }
    }
    const preview = template.replace(tokenPattern, (_, rawKey: string) =>
      resolveToken(rawKey.trim(), context),
    )
    if (preview.includes('{{') || preview.includes('}}')) throw new Error('名称模板包含未解析的参数')
    if (!preview.trim()) throw new Error('名称模板解析结果不能为空')
    if ([...preview].length > 100) throw new Error('名称解析结果不能超过 100 个字符')
    return { preview, error: '' }
  } catch (error) {
    return { preview: template, error: error instanceof Error ? error.message : '名称模板无效' }
  }
}

export function validateNameTemplate(
  template: string,
  context: NameTemplateContext,
): NameTemplateResult {
  if ((template.match(/\{\{/g)?.length ?? 0) !== (template.match(/\}\}/g)?.length ?? 0)) {
    return { preview: template, error: '名称模板包含未闭合的参数' }
  }
  return evaluateNameTemplate(template, context)
}

const serverAttributes = [
  'ID',
  'NAME',
  'COUNTRY',
  'COUNTRY_CODE',
  'COUNTRY_FLAG',
  'LOCATION',
  'ADDRESS',
]

export function getTemplateSuggestions(
  template: string,
  cursor: number,
  context: NameTemplateContext,
): { start: number; items: string[] } | null {
  const open = template.lastIndexOf('{{', cursor)
  if (open < 0 || template.lastIndexOf('}}', cursor) > open) return null
  const fragment = template.slice(open + 2, cursor).trimStart().toUpperCase()
  const globalItems = [
    ...serverAttributes,
    'SERVER',
    'SERVER_ID',
    'PROTOCOL',
    'PORT',
    'HOPS',
    ...context.servers[0]?.tags.map((_, index) => `TAG[${index}]`) ?? [],
  ]
  const scopedItems = context.servers.flatMap((server, index) => {
    const scopes = [
      ...(index === 0 ? ['ENTRY'] : []),
      ...(index === context.servers.length - 1 ? ['EXIT'] : []),
      `HOP[${index}]`,
    ]
    return scopes.flatMap((scope) => [
      ...serverAttributes.map((attribute) => `${scope}.${attribute}`),
      ...server.tags.map((_, tagIndex) => `${scope}.TAG[${tagIndex}]`),
    ])
  })
  const items = [...new Set([...globalItems, ...scopedItems])]
    .filter((item) => item.startsWith(fragment))
    .slice(0, 12)
  return items.length > 0 ? { start: open + 2, items } : null
}

export function parseTagInput(value: string): string[] {
  return value
    .split(/[,，]/)
    .map((tag) => tag.trim())
    .filter(Boolean)
}
