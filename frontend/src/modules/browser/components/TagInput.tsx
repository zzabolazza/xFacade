import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { X } from 'lucide-react'

interface TagInputProps {
  value: string[]
  onChange: (tags: string[]) => void
  suggestions?: string[]
  placeholder?: string
}

export function TagInput({ value, onChange, suggestions = [], placeholder = '输入标签后按回车' }: TagInputProps) {
  const [input, setInput] = useState('')
  const [showSuggestions, setShowSuggestions] = useState(false)
  const [dropdownStyle, setDropdownStyle] = useState<React.CSSProperties | null>(null)
  const wrapRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const filtered = suggestions.filter(
    s => s.toLowerCase().includes(input.toLowerCase()) && !value.includes(s)
  )

  const addTag = (tag: string) => {
    const t = tag.trim()
    if (!t || value.includes(t)) return
    onChange([...value, t])
    setInput('')
    setShowSuggestions(false)
  }

  const removeTag = (tag: string) => {
    onChange(value.filter(t => t !== tag))
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault()
      addTag(input)
    } else if (e.key === 'Backspace' && !input && value.length > 0) {
      removeTag(value[value.length - 1])
    } else if (e.key === 'Escape') {
      setShowSuggestions(false)
    }
  }

  // 点击外部关闭建议
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setShowSuggestions(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  useLayoutEffect(() => {
    if (!showSuggestions || filtered.length === 0) {
      setDropdownStyle(null)
      return
    }

    const updateDropdownStyle = () => {
      const rect = wrapRef.current?.getBoundingClientRect()
      if (!rect) return
      setDropdownStyle({
        position: 'fixed',
        left: rect.left,
        top: rect.bottom + 4,
        width: rect.width,
        maxHeight: Math.max(120, Math.min(260, window.innerHeight - rect.bottom - 16)),
      })
    }

    updateDropdownStyle()
    window.addEventListener('resize', updateDropdownStyle)
    window.addEventListener('scroll', updateDropdownStyle, true)
    return () => {
      window.removeEventListener('resize', updateDropdownStyle)
      window.removeEventListener('scroll', updateDropdownStyle, true)
    }
  }, [showSuggestions, filtered.length])

  return (
    <div ref={wrapRef} className="tag-input-wrap relative">
      <div
        className="min-h-9 flex flex-wrap gap-1.5 items-center px-3 py-1.5 rounded-md border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] cursor-text focus-within:border-[var(--color-accent)] transition-colors"
        onClick={() => inputRef.current?.focus()}
      >
        {value.map(tag => (
          <span
            key={tag}
            className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium bg-[var(--color-accent-muted)] text-[var(--color-accent)]"
          >
            {tag}
            <button
              type="button"
              onClick={e => { e.stopPropagation(); removeTag(tag) }}
              className="hover:text-[var(--color-error)] transition-colors"
            >
              <X className="w-3 h-3" />
            </button>
          </span>
        ))}
        <input
          ref={inputRef}
          value={input}
          onChange={e => { setInput(e.target.value); setShowSuggestions(true) }}
          onKeyDown={handleKeyDown}
          onFocus={() => setShowSuggestions(true)}
          placeholder={value.length === 0 ? placeholder : ''}
          className="flex-1 min-w-24 bg-transparent text-sm text-[var(--color-text-primary)] placeholder:text-[var(--color-text-muted)] outline-none"
        />
      </div>

      {showSuggestions && filtered.length > 0 && dropdownStyle && createPortal(
        <div
          style={dropdownStyle}
          className="z-[60] overflow-y-auto rounded-lg border border-[var(--color-border-default)] bg-[var(--color-bg-surface)] shadow-[var(--shadow-lg)] animate-scale-in"
        >
          {filtered.slice(0, 8).map(s => (
            <button
              key={s}
              type="button"
              onMouseDown={e => { e.preventDefault(); addTag(s) }}
              className="w-full text-left px-3 py-2 text-sm text-[var(--color-text-primary)] hover:bg-[var(--color-bg-muted)] transition-colors"
            >
              {s}
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  )
}
