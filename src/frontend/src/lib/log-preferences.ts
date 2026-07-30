import { useState } from 'react'

export type RefreshSeconds = 0 | 5 | 10 | 15 | 30 | 60
export type OperationPageSize = 10 | 20 | 50 | 100
export type RequestWindow = 10 | 30 | 50 | 100

export const REFRESH_OPTIONS: { value: RefreshSeconds; label: string }[] = [
  { value: 0, label: '不刷新' },
  { value: 5, label: '5 秒' },
  { value: 10, label: '10 秒' },
  { value: 15, label: '15 秒' },
  { value: 30, label: '30 秒' },
  { value: 60, label: '60 秒' },
]

export const OPERATION_PAGE_SIZE_OPTIONS: OperationPageSize[] = [10, 20, 50, 100]
export const REQUEST_WINDOW_OPTIONS: RequestWindow[] = [10, 30, 50, 100]

function readStoredNumber<T extends number>(key: string, fallback: T, allowed: readonly T[]): T {
  const value = Number(window.localStorage.getItem(key))
  return allowed.includes(value as T) ? (value as T) : fallback
}

export function useStoredNumber<T extends number>(key: string, fallback: T, allowed: readonly T[]) {
  const [value, setValue] = useState<T>(() => readStoredNumber(key, fallback, allowed))
  const update = (next: T) => {
    window.localStorage.setItem(key, String(next))
    setValue(next)
  }
  return [value, update] as const
}
