import { useState, type ComponentType, type CSSProperties } from 'react'
import { Check, Copy } from 'lucide-react'
import { cn } from '@cyber/theme'

declare const require: any

/* ---------- optional syntax highlighting ---------- */

let SyntaxHighlighter: ComponentType<any> | null = null
let hlStyles: { dark?: Record<string, any>; light?: Record<string, any> } = {}

try {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  SyntaxHighlighter = require('react-syntax-highlighter').Prism
  hlStyles = {
    dark: require('react-syntax-highlighter/dist/esm/styles/prism').oneDark,
    light: require('react-syntax-highlighter/dist/esm/styles/prism').oneLight,
  }
} catch {
  // react-syntax-highlighter is an optional peer dependency
}

/* -------------------------------------------------- */

export interface CodeBlockProps {
  code: string
  language?: string
  showLineNumbers?: boolean
  maxHeight?: number
  copyable?: boolean
  className?: string
}

const hlCustomStyle: CSSProperties = {
  margin: 0,
  padding: '0.75rem',
  fontSize: '0.75rem',
  background: 'transparent',
  borderRadius: 0,
}

const lineNumStyle: CSSProperties = {
  fontSize: '0.65rem',
  minWidth: '2em',
  opacity: 0.5,
}

export function CodeBlock({
  code,
  language,
  showLineNumbers = false,
  maxHeight,
  copyable = false,
  className,
}: CodeBlockProps) {
  const [copied, setCopied] = useState(false)
  const trimmed = code.replace(/\n$/, '')

  const handleCopy = async () => {
    await navigator.clipboard.writeText(trimmed)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const wrapClass = cn(
    'group relative overflow-hidden rounded-md border border-border bg-muted/40',
    className,
  )

  const scrollClass = cn('overflow-auto', maxHeight && `max-h-[${maxHeight}px]`)

  return (
    <div className={wrapClass}>
      {/* copy button */}
      {copyable && (
        <button
          type="button"
          onClick={handleCopy}
          className="absolute right-2 top-2 z-10 flex h-6 w-6 items-center justify-center rounded border border-border bg-card text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100"
          aria-label="Copy code"
        >
          {copied ? <Check className="h-3 w-3 text-success" /> : <Copy className="h-3 w-3" />}
        </button>
      )}

      <div className={scrollClass}>
        {SyntaxHighlighter && language ? (
          <SyntaxHighlighter
            language={language}
            style={hlStyles.dark}
            customStyle={hlCustomStyle}
            showLineNumbers={showLineNumbers}
            lineNumberStyle={lineNumStyle}
          >
            {trimmed}
          </SyntaxHighlighter>
        ) : (
          <pre className="p-3 font-mono text-xs leading-relaxed text-foreground">
            {trimmed}
          </pre>
        )}
      </div>
    </div>
  )
}
