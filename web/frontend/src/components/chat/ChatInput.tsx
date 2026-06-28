import { useState, useRef, useEffect, useCallback } from 'react'
import { Send, Slash } from 'lucide-react'
import { Button } from '@aspect/ui'
import { cn } from '@aspect/theme'

const COMMANDS = [
  { cmd: '/scan', desc: 'Start a scan', usage: '/scan <target> [--mode full] [--verify] [--sniper] [--deep]' },
  { cmd: '/status', desc: 'Show system status' },
  { cmd: '/agents', desc: 'List connected agents' },
  { cmd: '/help', desc: 'Show available commands' },
]

const inputContainerClass = 'w-full px-4 sm:px-5 lg:px-6'

interface Props {
  onSend: (content: string) => void
  disabled?: boolean
  placeholder?: string
  containerClassName?: string
  formClassName?: string
}

export default function ChatInput({ onSend, disabled, placeholder, containerClassName, formClassName }: Props) {
  const [draft, setDraft] = useState('')
  const [showHints, setShowHints] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const canSend = draft.trim().length > 0 && !disabled

  const handleSend = useCallback(() => {
    const text = draft.trim()
    if (!text || disabled) return
    onSend(text)
    setDraft('')
    setShowHints(false)
  }, [draft, disabled, onSend])

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
    if (e.key === 'Escape') {
      setShowHints(false)
    }
  }

  function handleChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    const value = e.target.value
    setDraft(value)
    setShowHints(value === '/' || (value.startsWith('/') && !value.includes(' ')))
  }

  function insertCommand(cmd: string) {
    setDraft(cmd + ' ')
    setShowHints(false)
    textareaRef.current?.focus()
  }

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }, [draft])

  const matchingCommands = draft.startsWith('/')
    ? COMMANDS.filter((c) => c.cmd.startsWith(draft.split(' ')[0]))
    : COMMANDS
  const containerClass = containerClassName || inputContainerClass

  return (
    <div className="relative border-t border-border bg-card/80 backdrop-blur-sm">
      {/* Command hints */}
      {showHints && (
        <div className="absolute bottom-full left-0 right-0 border-t border-border bg-card shadow-lg animate-in fade-in slide-in-from-bottom-1 duration-150">
          <div className={cn(containerClass, 'py-1.5')}>
            {matchingCommands.map((c) => (
              <button
                key={c.cmd}
                type="button"
                onClick={() => insertCommand(c.cmd)}
                className="flex w-full items-center gap-3 rounded-md px-2 py-1.5 text-left text-xs hover:bg-accent transition-colors"
              >
                <Slash className="h-3 w-3 shrink-0 text-primary" />
                <span className="font-mono font-medium text-foreground">{c.cmd}</span>
                <span className="text-muted-foreground">{c.desc}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      <div className={cn(containerClass, 'py-3')}>
        <div className={cn('flex items-end gap-2', formClassName)}>
          <textarea
            ref={textareaRef}
            rows={1}
            value={draft}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            onFocus={() => { if (draft === '/') setShowHints(true) }}
            onBlur={() => setTimeout(() => setShowHints(false), 150)}
            disabled={disabled}
            placeholder={placeholder || 'Type a message... (/ for commands)'}
            className={cn(
              'flex-1 resize-none rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground',
              'placeholder:text-muted-foreground',
              'focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary/50',
              'disabled:cursor-not-allowed disabled:opacity-50',
              'transition-shadow duration-150',
            )}
          />
          <Button
            size="icon"
            onClick={handleSend}
            disabled={!canSend}
            className={cn(
              'h-9 w-9 shrink-0 transition-all duration-150',
              canSend && 'bg-primary hover:bg-primary shadow-sm shadow-primary/25',
            )}
            aria-label="Send message"
          >
            <Send className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  )
}
