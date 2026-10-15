import type { LogSeverity } from './types'

/** Values offered by the operation logs severity filter. */
const FILTERABLE_SEVERITIES: readonly LogSeverity[] = ['info', 'warning', 'error']

/**
 * Parse an initial severity filter from a location search string
 * (e.g. `?severity=error`, with or without the leading `?`).
 * Only the values offered by the operation logs severity filter are
 * accepted; anything else maps to `''`.
 */
export function initialSeverityFromSearch(search: string): LogSeverity | '' {
  const value = new URLSearchParams(search).get('severity') ?? ''
  return FILTERABLE_SEVERITIES.find((severity) => severity === value) ?? ''
}

/**
 * Return the search string without the `severity` param, keeping unrelated
 * params (and the leading `?` when params remain) intact. Returns the input
 * unchanged when no `severity` param is present.
 */
export function stripSeverityParam(search: string): string {
  const params = new URLSearchParams(search)
  if (!params.has('severity')) return search
  params.delete('severity')
  const next = params.toString()
  return next ? `?${next}` : ''
}
