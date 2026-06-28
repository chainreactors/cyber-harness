import { useState, useRef, useEffect, useCallback } from 'react'
import { Send, Square } from 'lucide-react'
import { Button } from '@aspect/ui'
import { cn } from '@aspect/theme'

const inputContainerClass = 'w-full px-4 sm:px-5 lg:px-6'

interface Props {
  onSend: (content: string) => void
  onPause?: () => void
  busy?: boolean
  disabled?: boolean
  placeholder?: string
  containerClassName?: string
  formClassName?: string
}

export default function ChatInput({ onSend, onPause, busy, disabled, placeholder, containerClassName, formClassName }: Props) {
  const [draft, setDraft] = useState('')
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  const canSend = draft.trim().length > 0 && !disabled
  const canPause = !!busy && !disabled && !!onPause

  const handleSend = useCallback(() => {
    const text = draft.trim()
    if (!text || disabled) return
    onSend(text)
    setDraft('')
  }, [draft, disabled, onSend])

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
    if (e.key === 'Escape') {
      textareaRef.current?.blur()
    }
  }

  function handleChange(e: React.ChangeEvent<HTMLTextAreaElement>) {
    setDraft(e.target.value)
  }

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }, [draft])

  const containerClass = containerClassName || inputContainerClass

  return (
    <div className="relative border-t border-border bg-card/80 backdrop-blur-sm">
      <div className={cn(containerClass, 'py-3')}>
        <div className={cn('flex items-end gap-2', formClassName)}>
          <textarea
            ref={textareaRef}
            rows={1}
            value={draft}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            disabled={disabled}
            placeholder={placeholder || 'Type a message...'}
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
            onClick={canPause ? onPause : handleSend}
            disabled={canPause ? false : !canSend}
            className={cn(
              'h-9 w-9 shrink-0 transition-all duration-150',
              canPause
                ? 'bg-destructive hover:bg-destructive shadow-sm shadow-destructive/20'
                : canSend && 'bg-primary hover:bg-primary shadow-sm shadow-primary/25',
            )}
            aria-label={canPause ? 'Pause response' : 'Send message'}
          >
            {canPause ? <Square className="h-4 w-4 fill-current" /> : <Send className="h-4 w-4" />}
          </Button>
        </div>
      </div>
    </div>
  )
}
