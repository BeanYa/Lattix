export const DEFAULT_NODE_NAME_TEMPLATE = '{{LOCATION}}-{{PROTOCOL}}-inbound'
export const DEFAULT_CHAIN_NAME_TEMPLATE = '{{ENTRY}}-{{EXIT}}-{{PROTOCOL}}-chain'

export interface NameTemplateValues {
  location: string
  serverId?: number
  protocol: string
  port?: string
  entry?: string
  entryId?: number
  exit?: string
  exitId?: number
  hops?: number
  tags: string[]
}

export function renderNameTemplate(template: string, values: NameTemplateValues): string {
  return template.replace(/\{\{\s*([A-Z][A-Z0-9_]*)\s*\}\}/g, (_, key: string) => {
    if (key === 'LOCATION' || key === 'SERVER') return values.location
    if (key === 'SERVER_ID') return values.serverId ? String(values.serverId) : '?'
    if (key === 'PROTOCOL') return values.protocol
    if (key === 'PORT') return values.port || 'auto'
    if (key === 'ENTRY') return values.entry || '?'
    if (key === 'ENTRY_ID') return values.entryId ? String(values.entryId) : '?'
    if (key === 'EXIT') return values.exit || '?'
    if (key === 'EXIT_ID') return values.exitId ? String(values.exitId) : '?'
    if (key === 'HOPS') return values.hops ? String(values.hops) : '?'
    if (key.startsWith('TAG_')) {
      const index = Number(key.slice(4))
      return Number.isInteger(index) && index > 0 ? values.tags[index - 1] || '?' : '?'
    }
    return `{{${key}}}`
  })
}

export function parseTagInput(value: string): string[] {
  return value
    .split(/[,，]/)
    .map((tag) => tag.trim())
    .filter(Boolean)
}
