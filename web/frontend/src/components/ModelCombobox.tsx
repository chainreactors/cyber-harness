import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, ChevronDown, RefreshCw } from 'lucide-react'
import { Button, Input, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { cn } from '@cyber/theme'

interface ModelComboboxProps {
  value: string
  onChange: (value: string) => void
  models: string[]
  loading?: boolean
  error?: string | null
  onRefresh?: () => void
  placeholder?: string
}

/** Wrap the first case-insensitive match of `query` inside `text` in an AI-accent mark. */
function highlight(text: string, query: string): ReactNode {
  if (!query) return text
  const idx = text.toLowerCase().indexOf(query.toLowerCase())
  if (idx < 0) return text
  return (
    <>
      {text.slice(0, idx)}
      <span className="rounded-[2px] bg-ai/25 text-foreground">{text.slice(idx, idx + query.length)}</span>
      {text.slice(idx + query.length)}
    </>
  )
}

/**
 * Editable model picker: free-text entry plus a styled, filter-as-you-type list of
 * fetched models. Replaces the native <datalist>, which cannot be themed. Model IDs
 * render in the mono face — they are identifiers, and the console treatment is the
 * one deliberate signature here.
 */
export function ModelCombobox({ value, onChange, models, loading = false, error, onRefresh, placeholder }: ModelComboboxProps) {
  const { t } = useTranslation('config')
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const wrapRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const wasLoading = useRef(false)
  const wasOpen = useRef(false)

  const query = value.trim()
  // The input doubles as the filter. A committed value (exact match) shows the full
  // list so re-opening lets you browse, not just see the one row you already picked.
  const isExact = useMemo(() => models.some((m) => m.toLowerCase() === query.toLowerCase()), [models, query])
  const filtering = query.length > 0 && !isExact
  const filtered = useMemo(() => {
    if (!filtering) return models
    const q = query.toLowerCase()
    return models.filter((m) => m.toLowerCase().includes(q))
  }, [models, query, filtering])

  // On a clean fetch, open straight onto the list so the operator sees the result.
  // Failures stay quiet here — the inline message below the field carries them.
  useEffect(() => {
    if (wasLoading.current && !loading && !error && models.length > 0) setOpen(true)
    wasLoading.current = loading
  }, [loading, error, models.length])

  // Keep the highlighted row within bounds as the filter narrows.
  useEffect(() => {
    setActive((i) => Math.min(Math.max(i, 0), Math.max(filtered.length - 1, 0)))
  }, [filtered.length])

  // On open, start the highlight on the current selection so keyboard nav and the
  // eye both begin where the operator already is. Only on the closed→open edge, so
  // typing (which resets to the top match) is left alone.
  useEffect(() => {
    if (open && !wasOpen.current) {
      const idx = filtered.findIndex((m) => m === value)
      setActive(idx >= 0 ? idx : 0)
    }
    wasOpen.current = open
  }, [open, filtered, value])

  // Keyboard navigation should never scroll the active row out of sight.
  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [active, open])

  // Close on any click outside the control (covers the pointer path; onBlur covers Tab).
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const commit = (model: string) => {
    onChange(model)
    setOpen(false)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        if (!open) setOpen(true)
        else setActive((i) => Math.min(i + 1, filtered.length - 1))
        break
      case 'ArrowUp':
        e.preventDefault()
        setActive((i) => Math.max(i - 1, 0))
        break
      case 'Enter':
        // The config panel is a <form>; a bare Enter here would submit it. Swallow it
        // whenever the list is open, committing the highlighted model if there is one.
        if (open) {
          e.preventDefault()
          if (filtered[active]) commit(filtered[active])
          else setOpen(false)
        }
        break
      case 'Escape':
        if (open) {
          e.preventDefault()
          e.stopPropagation()
          setOpen(false)
        }
        break
    }
  }

  return (
    <div
      ref={wrapRef}
      className="relative flex items-center gap-2"
      onBlur={(e) => {
        if (!wrapRef.current?.contains(e.relatedTarget as Node)) setOpen(false)
      }}
    >
      <div className="relative flex-1">
        <Input
          ref={inputRef}
          value={value}
          onChange={(e) => {
            onChange(e.target.value)
            setActive(0)
            if (models.length) setOpen(true)
          }}
          onFocus={() => {
            if (models.length) setOpen(true)
          }}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          className="pr-9 font-mono"
          role="combobox"
          aria-expanded={open}
          aria-autocomplete="list"
        />
        <button
          type="button"
          tabIndex={-1}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => setOpen((o) => !o)}
          className="absolute right-0 top-0 flex h-10 w-9 items-center justify-center text-muted-foreground/70 transition-colors hover:text-foreground"
          aria-hidden
        >
          <ChevronDown className={cn('h-4 w-4 transition-transform duration-200', open && 'rotate-180')} />
        </button>
      </div>

      {onRefresh && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="shrink-0"
              onMouseDown={(e) => e.preventDefault()}
              onClick={onRefresh}
              disabled={loading}
            >
              <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />
            </Button>
          </TooltipTrigger>
          <TooltipContent>{t('fetchModels')}</TooltipContent>
        </Tooltip>
      )}

      {open && (
        <div className="absolute left-0 right-0 top-full z-50 mt-1.5 overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-md animate-in fade-in-0 zoom-in-95 duration-150">
          <div className="flex items-center justify-between border-b border-border/60 px-2.5 py-1.5">
            <span className="mono-label text-[10px] text-muted-foreground">
              {filtering ? `${filtered.length} / ${models.length}` : t('modelsCount', { count: models.length })}
            </span>
          </div>

          {loading ? (
            <div className="flex items-center gap-2 px-3 py-4 text-xs text-muted-foreground">
              <RefreshCw className="h-3.5 w-3.5 animate-spin" />
              {t('modelsLoading')}
            </div>
          ) : error ? (
            <div className="px-3 py-4 text-xs text-destructive">{error}</div>
          ) : models.length === 0 ? (
            <button
              type="button"
              tabIndex={-1}
              onMouseDown={(e) => e.preventDefault()}
              onClick={onRefresh}
              className="flex w-full flex-col items-start gap-1.5 px-3 py-4 text-left transition-colors hover:bg-accent"
            >
              <span className="text-xs text-muted-foreground">{t('modelsNotFetched')}</span>
              <span className="flex items-center gap-1.5 text-xs text-ai">
                <RefreshCw className="h-3 w-3" />
                {t('fetchModels')}
              </span>
            </button>
          ) : filtered.length === 0 ? (
            <div className="px-3 py-3">
              <p className="text-xs text-muted-foreground">{t('modelSearchNoMatch', { query })}</p>
              <p className="mt-1 text-xs text-foreground/70">{t('modelUseCustom', { query })}</p>
            </div>
          ) : (
            <div ref={listRef} className="max-h-60 overflow-y-auto p-1">
              {filtered.map((m, i) => (
                <button
                  key={m}
                  type="button"
                  tabIndex={-1}
                  data-active={i === active}
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setActive(i)}
                  onClick={() => commit(m)}
                  className={cn(
                    'relative flex w-full items-center rounded-sm py-1.5 pl-8 pr-2 text-left font-mono text-[13px] outline-none transition-colors',
                    i === active ? 'bg-accent text-accent-foreground' : 'text-foreground/90'
                  )}
                >
                  {m === value && <Check className="absolute left-2 h-3.5 w-3.5 text-ai" />}
                  <span className="truncate">{filtering ? highlight(m, query) : m}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
