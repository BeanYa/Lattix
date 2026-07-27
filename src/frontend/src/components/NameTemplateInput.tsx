import { useEffect, useRef, useState } from 'react'
import { XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { evaluateNameTemplate, getTemplateSuggestions, type NameTemplateContext } from '@/lib/naming'
import { cn } from '@/lib/utils'

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

function tokenLabel(value: string): string {
  return value.slice(2, -2).trim()
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
  const parts = splitTemplate(value)
  const isEmpty = !value.trim()
  const result =
    allowEmpty && isEmpty ? { preview: '', error: '' } : evaluateNameTemplate(value, context)
  const suggestions = getTemplateSuggestions(value, cursor, context)
  const visibleSuggestions = dismissed ? null : suggestions
  const suggestionKey = suggestions?.items.join('|') ?? ''

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
    replaceRange(visibleSuggestions.start, cursor, `${item}}}`)
  }

  const removeToken = (part: TemplatePart) => {
    replaceRange(part.start, part.end, '')
  }

  const focusLastTextPart = () => {
    const textInputs = editorRef.current?.querySelectorAll<HTMLInputElement>('[data-template-text]')
    const target = textInputs?.item((textInputs?.length ?? 1) - 1)
    if (!target) return
    target.focus()
    target.setSelectionRange(target.value.length, target.value.length)
    setCursor(Number(target.dataset.end))
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
        className={cn(
          'flex min-h-8 w-full cursor-text flex-wrap items-center gap-1 rounded-lg border border-input bg-transparent px-2 py-1 text-sm transition-colors outline-none focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50',
          result.error && 'border-destructive ring-3 ring-destructive/20',
        )}
        onClick={(event) => {
          if (event.target === event.currentTarget) focusLastTextPart()
        }}
      >
        {parts.map((part, index) =>
          part.kind === 'token' ? (
            <span
              key={`${part.start}-${part.value}`}
              data-template-token
              data-template-value={part.value}
              className="inline-flex h-6 shrink-0 items-center gap-0.5 rounded-md border border-primary/20 bg-primary/10 pl-2 pr-1 font-mono text-xs text-primary"
            >
              {tokenLabel(part.value)}
              <button
                type="button"
                className="inline-flex size-4 items-center justify-center rounded-sm text-primary/70 hover:bg-primary/15 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
              className="h-6 min-w-[1ch] max-w-full bg-transparent px-0.5 outline-none placeholder:text-muted-foreground"
              style={{ width: `${textWidth(part.value)}ch` }}
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
        <div className="flex flex-wrap gap-1 rounded-md border bg-muted/40 p-2" role="listbox">
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
              {item}
            </Button>
          ))}
        </div>
      ) : null}
      <p className={result.error ? 'text-xs text-destructive' : 'text-xs text-muted-foreground'}>
        {result.error || (allowEmpty && isEmpty ? emptyHint : `预览：${result.preview}`)}
      </p>
    </div>
  )
}
