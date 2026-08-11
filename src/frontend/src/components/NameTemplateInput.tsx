import { useEffect, useRef, useState } from 'react'
import { XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { evaluateNameTemplate, getTemplateSuggestions, type NameTemplateContext } from '@/lib/naming'
import { cn } from '@/lib/utils'

import './NameTemplateInput.css'

interface NameTemplateInputProps {
  id?: string
  value: string
  onChange: (value: string) => void
  context: NameTemplateContext
  allowEmpty?: boolean
  emptyHint?: string
  placeholder?: string
}

interface TemplatePart {
  kind: 'text' | 'token'
  value: string
  start: number
  end: number
}

const completeTokenPattern = /\{\{[^{}]+\}\}/g

function splitTemplate(value: string): TemplatePart[] {
  const parts: TemplatePart[] = []
  let cursor = 0

  for (const match of value.matchAll(completeTokenPattern)) {
    const start = match.index
    parts.push({ kind: 'text', value: value.slice(cursor, start), start: cursor, end: start })
    parts.push({
      kind: 'token',
      value: match[0],
      start,
      end: start + match[0].length,
    })
    cursor = start + match[0].length
  }

  parts.push({ kind: 'text', value: value.slice(cursor), start: cursor, end: value.length })
  return parts
}

function textWidth(value: string): number {
  return Math.max(
    1,
    [...value].reduce((width, character) => width + (character.charCodeAt(0) > 255 ? 2 : 1), 0),
  )
}

function templateKeyLabel(value: string): string {
  return value.replace(/^HOP\[(\d+)\]/, 'HOP_$1')
}

function tokenLabel(value: string): string {
  return templateKeyLabel(value.slice(2, -2).trim())
}

export function NameTemplateInput({
  id,
  value,
  onChange,
  context,
  allowEmpty = false,
  emptyHint,
  placeholder,
}: NameTemplateInputProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const [cursor, setCursor] = useState(value.length)
  const [suggestionIndex, setSuggestionIndex] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const [focused, setFocused] = useState(false)
  const parts = splitTemplate(value)
  const isEmpty = !value.trim()
  const result =
    allowEmpty && isEmpty ? { preview: '', error: '' } : evaluateNameTemplate(value, context)
  const suggestions = getTemplateSuggestions(value, cursor, context)
  const defaultSuggestions = getTemplateSuggestions('{{', 2, context)
  const visibleSuggestions = dismissed
    ? null
    : suggestions ?? (focused ? defaultSuggestions : null)
  const suggestionKey = `${suggestions ? 'filter' : 'all'}:${visibleSuggestions?.items.join('|') ?? ''}`

  useEffect(() => {
    setSuggestionIndex(0)
  }, [suggestionKey])

  useEffect(() => {
    setCursor((current) => Math.min(current, value.length))
  }, [value.length])

  const focusPosition = (nextValue: string, position: number) => {
    requestAnimationFrame(() => {
      const textParts = [...(editorRef.current?.querySelectorAll<HTMLInputElement>('[data-template-text]') ?? [])]
      const target =
        textParts.find((input) => {
          const start = Number(input.dataset.start)
          const end = Number(input.dataset.end)
          return position >= start && position <= end
        }) ?? textParts.at(-1)
      if (!target) return

      const start = Number(target.dataset.start)
      const localCursor = Math.max(0, Math.min(position - start, target.value.length))
      target.focus()
      target.setSelectionRange(localCursor, localCursor)
      setCursor(Math.min(position, nextValue.length))
    })
  }

  const replaceRange = (start: number, end: number, replacement: string) => {
    const next = `${value.slice(0, start)}${replacement}${value.slice(end)}`
    if (next.length > 200) return
    onChange(next)
    setDismissed(true)
    focusPosition(next, start + replacement.length)
  }

  const choose = (item: string) => {
    if (!visibleSuggestions) return
    if (suggestions) {
      replaceRange(visibleSuggestions.start, cursor, `${item}}}`)
    } else {
      replaceRange(cursor, cursor, `{{${item}}}`)
    }
  }

  const removeToken = (part: TemplatePart) => {
    replaceRange(part.start, part.end, '')
  }

  const focusNearestTextPart = (clientX: number) => {
    const textParts = [
      ...(editorRef.current?.querySelectorAll<HTMLInputElement>('[data-template-text]') ?? []),
    ]
    if (!textParts.length) return
    let target = textParts[0]
    let minDistance = Number.POSITIVE_INFINITY
    for (const input of textParts) {
      const rect = input.getBoundingClientRect()
      const distance =
        clientX < rect.left ? rect.left - clientX : clientX > rect.right ? clientX - rect.right : 0
      if (distance < minDistance) {
        minDistance = distance
        target = input
      }
    }
    const rect = target.getBoundingClientRect()
    const localCursor = clientX > (rect.left + rect.right) / 2 ? target.value.length : 0
    target.focus()
    target.setSelectionRange(localCursor, localCursor)
    setCursor(Number(target.dataset.start) + localCursor)
    setDismissed(false)
  }

  return (
    <div className="flex flex-col gap-2">
      <div
        id={id}
        ref={editorRef}
        role="textbox"
        aria-multiline="false"
        aria-invalid={Boolean(result.error)}
        className={cn('cg-template-editor', result.error && 'is-error')}
        onClick={(event) => {
          if (event.target === event.currentTarget) focusNearestTextPart(event.clientX)
        }}
      >
        {parts.map((part, index) =>
          part.kind === 'token' ? (
            <span
              key={`${part.start}-${part.value}`}
              data-template-token
              data-template-value={part.value}
              className="cg-template-token"
            >
              {tokenLabel(part.value)}
              <button
                type="button"
                className="cg-template-token-remove"
                aria-label={`删除变量 ${tokenLabel(part.value)}`}
                onClick={() => removeToken(part)}
              >
                <XIcon className="size-3" />
              </button>
            </span>
          ) : (
            <input
              key={`${part.start}-text-${index}`}
              data-template-text
              data-start={part.start}
              data-end={part.end}
              aria-label="模板文本"
              value={part.value}
              placeholder={isEmpty ? placeholder : undefined}
              autoFocus={index === 0}
              className={cn(
                'cg-template-text',
                parts.length === 1
                  ? 'min-w-0 flex-1'
                  : part.value
                    ? 'min-w-[1ch] shrink-0'
                    : 'min-w-0 shrink-0',
              )}
              style={
                parts.length === 1
                  ? undefined
                  : {
                      width: part.value ? `calc(${textWidth(part.value)}ch + 2px)` : '4px',
                    }
              }
              onFocus={(event) => {
                setFocused(true)
                setCursor(part.start + (event.currentTarget.selectionStart ?? part.value.length))
              }}
              onBlur={() => setFocused(false)}
              onChange={(event) => {
                const replacement = event.target.value
                const next = `${value.slice(0, part.start)}${replacement}${value.slice(part.end)}`
                if (next.length > 200) return
                const nextCursor = part.start + (event.target.selectionStart ?? replacement.length)
                onChange(next)
                setCursor(nextCursor)
                setDismissed(false)
              }}
              onClick={(event) => {
                setCursor(part.start + (event.currentTarget.selectionStart ?? part.value.length))
                setDismissed(false)
              }}
              onKeyUp={(event) => {
                setCursor(part.start + (event.currentTarget.selectionStart ?? part.value.length))
              }}
              onKeyDown={(event) => {
                if (visibleSuggestions) {
                  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                    event.preventDefault()
                    const direction = event.key === 'ArrowDown' ? 1 : -1
                    setSuggestionIndex(
                      (current) =>
                        (current + direction + visibleSuggestions.items.length) %
                        visibleSuggestions.items.length,
                    )
                    return
                  }
                  if (event.key === 'Enter' || event.key === 'Tab') {
                    event.preventDefault()
                    choose(visibleSuggestions.items[suggestionIndex])
                    return
                  }
                  if (event.key === 'Escape') {
                    event.preventDefault()
                    setDismissed(true)
                    return
                  }
                }

                const selectionStart = event.currentTarget.selectionStart
                const selectionEnd = event.currentTarget.selectionEnd
                if (selectionStart !== selectionEnd) return
                if (event.key === 'Backspace' && selectionStart === 0) {
                  const previous = parts[index - 1]
                  if (previous?.kind === 'token') {
                    event.preventDefault()
                    removeToken(previous)
                  }
                } else if (event.key === 'Delete' && selectionStart === part.value.length) {
                  const next = parts[index + 1]
                  if (next?.kind === 'token') {
                    event.preventDefault()
                    removeToken(next)
                  }
                }
              }}
            />
          ),
        )}
      </div>
      {visibleSuggestions ? (
        <div className="cg-template-suggestions" role="listbox">
          {visibleSuggestions.items.map((item, index) => (
            <Button
              key={item}
              type="button"
              variant={index === suggestionIndex ? 'secondary' : 'ghost'}
              size="xs"
              role="option"
              aria-selected={index === suggestionIndex}
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => choose(item)}
            >
              {templateKeyLabel(item)}
            </Button>
          ))}
        </div>
      ) : null}
      <p className={cn('cg-template-hint', result.error && 'is-error')}>
        {result.error || (allowEmpty && isEmpty ? emptyHint : `预览：${result.preview}`)}
      </p>
    </div>
  )
}
