import { useState } from 'react'
import { TagIcon, XIcon } from 'lucide-react'

interface TagInputProps {
  id?: string
  value: string[]
  onChange: (value: string[]) => void
  maxTags?: number
  placeholder?: string
}

export function TagInput({
  id,
  value,
  onChange,
  maxTags = 10,
  placeholder = '输入标签后按回车',
}: TagInputProps) {
  const [draft, setDraft] = useState('')

  const addTags = (input: string) => {
    const candidates = input
      .split(/[,，]/)
      .map((tag) => tag.trim())
      .filter(Boolean)
    if (candidates.length === 0) return
    onChange([...value, ...candidates].slice(0, maxTags))
    setDraft('')
  }

  const removeTag = (index: number) => {
    onChange(value.filter((_, tagIndex) => tagIndex !== index))
  }

  return (
    <div
      className="flex min-h-8 w-full flex-wrap items-center gap-1 rounded-lg border border-input bg-transparent px-2 py-1 text-sm transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50"
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          event.currentTarget.querySelector('input')?.focus()
        }
      }}
    >
      <TagIcon className="mr-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      {value.map((tag, index) => (
        <span
          key={`${tag}-${index}`}
          className="inline-flex h-6 items-center gap-0.5 rounded-md border border-primary/20 bg-primary/10 pl-2 pr-1 text-xs text-primary"
        >
          {tag}
          <button
            type="button"
            className="inline-flex size-4 items-center justify-center rounded-sm text-primary/70 hover:bg-primary/15 hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={`删除标签 ${tag}`}
            onClick={() => removeTag(index)}
          >
            <XIcon className="size-3" />
          </button>
        </span>
      ))}
      <input
        id={id}
        value={draft}
        disabled={value.length >= maxTags}
        className="h-6 min-w-24 flex-1 bg-transparent px-1 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed"
        placeholder={value.length >= maxTags ? `最多 ${maxTags} 个标签` : placeholder}
        aria-label="新标签"
        onChange={(event) => {
          const next = event.target.value
          if (/[,，]/.test(next)) {
            addTags(next)
          } else {
            setDraft(next)
          }
        }}
        onBlur={() => addTags(draft)}
        onPaste={(event) => {
          const pasted = event.clipboardData.getData('text')
          if (!/[,，]/.test(pasted)) return
          event.preventDefault()
          addTags(`${draft}${pasted}`)
        }}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === 'Tab') {
            if (!draft.trim()) return
            event.preventDefault()
            addTags(draft)
          } else if (event.key === 'Backspace' && !draft && value.length > 0) {
            removeTag(value.length - 1)
          }
        }}
      />
    </div>
  )
}
