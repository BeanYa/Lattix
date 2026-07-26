import { useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { evaluateNameTemplate, getTemplateSuggestions, type NameTemplateContext } from '@/lib/naming'

interface NameTemplateInputProps {
  id?: string
  value: string
  onChange: (value: string) => void
  context: NameTemplateContext
}

export function NameTemplateInput({ id, value, onChange, context }: NameTemplateInputProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [cursor, setCursor] = useState(value.length)
  const [suggestionIndex, setSuggestionIndex] = useState(0)
  const [dismissed, setDismissed] = useState(false)
  const result = evaluateNameTemplate(value, context)
  const suggestions = getTemplateSuggestions(value, cursor, context)
  const visibleSuggestions = dismissed ? null : suggestions
  const suggestionKey = suggestions?.items.join('|') ?? ''

  useEffect(() => {
    setSuggestionIndex(0)
  }, [suggestionKey])

  const choose = (item: string) => {
    if (!visibleSuggestions) return
    const next = `${value.slice(0, visibleSuggestions.start)}${item}}}${value.slice(cursor)}`
    const nextCursor = visibleSuggestions.start + item.length + 2
    onChange(next)
    setDismissed(true)
    requestAnimationFrame(() => {
      inputRef.current?.focus()
      inputRef.current?.setSelectionRange(nextCursor, nextCursor)
      setCursor(nextCursor)
    })
  }

  return (
    <div className="flex flex-col gap-2">
      <Input
        id={id}
        ref={inputRef}
        value={value}
        onChange={(event) => {
          onChange(event.target.value)
          setCursor(event.target.selectionStart ?? event.target.value.length)
          setDismissed(false)
        }}
        onClick={(event) => {
          setCursor(event.currentTarget.selectionStart ?? value.length)
          setDismissed(false)
        }}
        onKeyDown={(event) => {
          if (!visibleSuggestions) return
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault()
            const direction = event.key === 'ArrowDown' ? 1 : -1
            setSuggestionIndex(
              (current) =>
                (current + direction + visibleSuggestions.items.length) %
                visibleSuggestions.items.length,
            )
          } else if (event.key === 'Enter' || event.key === 'Tab') {
            event.preventDefault()
            choose(visibleSuggestions.items[suggestionIndex])
          } else if (event.key === 'Escape') {
            event.preventDefault()
            setDismissed(true)
          }
        }}
        onKeyUp={(event) => setCursor(event.currentTarget.selectionStart ?? value.length)}
        maxLength={200}
        aria-invalid={Boolean(result.error)}
        required
        autoFocus
      />
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
              onClick={() => choose(item)}
            >
              {item}
            </Button>
          ))}
        </div>
      ) : null}
      <p className={result.error ? 'text-xs text-destructive' : 'text-xs text-muted-foreground'}>
        {result.error || `预览：${result.preview}`}
      </p>
    </div>
  )
}
