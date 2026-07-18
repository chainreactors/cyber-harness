import { useMemo, type ComponentType, type ReactNode } from 'react'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { cn } from '@cyber/theme'

declare const require: any

interface Props {
  content: string
  compact?: boolean
  muted?: boolean
  inverted?: boolean
  isDark?: boolean
  /** Show anchor links on headings (hover to reveal) */
  headingAnchors?: boolean
  className?: string
}

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

/* ---------- heading anchor helpers ---------- */

function nodeText(node: ReactNode): string {
  if (node == null || typeof node === 'boolean') return ''
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(nodeText).join('')
  if (typeof node === 'object' && 'props' in node) {
    return nodeText((node as { props?: { children?: ReactNode } }).props?.children)
  }
  return ''
}

function anchorSlug(value: string) {
  const slug = value
    .trim()
    .toLowerCase()
    .replace(/<[^>]*>/g, '')
    .replace(/&[a-z0-9#]+;/g, '')
    .replace(/[^a-z0-9一-龥]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return (slug || 'section').slice(0, 96)
}

function uniqueAnchorSlug(value: string, slugCounts: Map<string, number>) {
  const base = anchorSlug(value)
  const count = slugCounts.get(base) || 0
  slugCounts.set(base, count + 1)
  return count === 0 ? base : `${base}-${count + 1}`
}

type HeadingTag = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

function anchoredHeading(
  Tag: HeadingTag,
  slugCounts: Map<string, number>,
  compact: boolean,
  inverted: boolean,
  muted: boolean,
) {
  const sizeClass: Record<HeadingTag, string> = {
    h1: compact ? 'text-sm mt-1.5 mb-1' : 'text-[22px] mt-5 mb-2',
    h2: compact ? 'text-sm mt-1.5 mb-1' : 'text-h2 mt-5 mb-2',
    h3: compact ? 'text-xs mt-1.5 mb-1' : 'text-h3 mt-4 mb-1.5',
    h4: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
    h5: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
    h6: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
  }

  return function Heading({ children }: { children?: ReactNode }) {
    const text = nodeText(children)
    const id = uniqueAnchorSlug(text || 'section', slugCounts)

    return (
      <Tag
        id={id}
        className={cn(
          'group scroll-mt-24 font-semibold leading-tight',
          sizeClass[Tag],
          inverted ? 'text-background' : muted ? 'text-muted-foreground' : 'text-foreground',
        )}
      >
        {children}
        <a
          href={`#${id}`}
          aria-label={`Link to ${text}`}
          className="ml-2 inline-flex align-middle text-muted-foreground opacity-0 transition-opacity group-hover:opacity-70 hover:!opacity-100"
        >
          #
        </a>
      </Tag>
    )
  }
}

function plainHeading(
  Tag: HeadingTag,
  compact: boolean,
  inverted: boolean,
  muted: boolean,
) {
  const sizeClass: Record<HeadingTag, string> = {
    h1: compact ? 'text-sm mt-1.5 mb-1' : 'text-[22px] mt-5 mb-2',
    h2: compact ? 'text-sm mt-1.5 mb-1' : 'text-h2 mt-5 mb-2',
    h3: compact ? 'text-xs mt-1.5 mb-1' : 'text-h3 mt-4 mb-1.5',
    h4: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
    h5: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
    h6: compact ? 'text-xs mt-1.5 mb-1' : 'text-h4 mt-4 mb-1.5',
  }

  return ({ children }: { children?: ReactNode }) => (
    <Tag
      className={cn(
        'font-semibold leading-tight',
        sizeClass[Tag],
        inverted ? 'text-background' : muted ? 'text-muted-foreground' : 'text-foreground',
      )}
    >
      {children}
    </Tag>
  )
}

/* ---------- component ---------- */

export function MarkdownContent({
  content,
  compact = false,
  muted = false,
  inverted = false,
  headingAnchors = false,
  className,
}: Props) {
  const headingSlugs = useMemo(() => new Map<string, number>(), [content])

  const components: Components = useMemo(() => {
    const headings = (['h1', 'h2', 'h3', 'h4', 'h5', 'h6'] as const).reduce(
      (acc, tag) => {
        acc[tag] = headingAnchors
          ? anchoredHeading(tag, headingSlugs, compact, inverted, muted)
          : plainHeading(tag, compact, inverted, muted)
        return acc
      },
      {} as Record<string, any>,
    )

    return {
      ...headings,
      p: ({ children }) => (
        <p className={cn(compact ? 'my-1 leading-relaxed' : 'my-3 leading-[1.65]')}>{children}</p>
      ),
      a: ({ children, href }) => (
        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          className="text-primary no-underline hover:underline"
        >
          {children}
        </a>
      ),
      ul: ({ children }) => (
        <ul className={cn('list-disc pl-5', compact ? 'my-1' : 'my-3 space-y-1.5')}>
          {children}
        </ul>
      ),
      ol: ({ children }) => (
        <ol className={cn('list-decimal pl-5', compact ? 'my-1' : 'my-3 space-y-1.5')}>
          {children}
        </ol>
      ),
      li: ({ children }) => (
        <li className={cn(compact ? 'my-0.5' : 'my-1')}>{children}</li>
      ),
      blockquote: ({ children }) => (
        <blockquote
          className={cn(
            'border-l-[3px] pl-2.5',
            compact
              ? 'my-1.5 border-primary'
              : 'my-3 rounded-r-md border-border bg-muted/30 py-1.5 pr-3',
            inverted ? 'text-muted' : 'text-muted-foreground',
          )}
        >
          {children}
        </blockquote>
      ),
      pre: ({ children }) => (
        <pre
          className={cn(
            'overflow-x-auto rounded-md border border-border leading-relaxed',
            compact ? 'my-1.5 bg-input p-2' : 'my-4 bg-muted/40 p-3',
          )}
        >
          {children}
        </pre>
      ),
      code: ({ className: codeClassName, children }) => {
        const langMatch = codeClassName?.match(/language-(\w+)/)
        // A fenced block with no info string gets no className from react-markdown,
        // so key "block vs inline" off the className OR a contained newline: a
        // multi-line run is always a block (e.g. an agent's ``` … ``` panel dump,
        // or a REPL /status box). Without this it falls to inline-code styling and
        // renders as a pill nested in the <pre> instead of a clean monospace block.
        const isBlock = Boolean(codeClassName) || String(children).includes('\n')

        if (isBlock && SyntaxHighlighter && langMatch) {
          const lang = langMatch[1]
          const codeStr = String(children).replace(/\n$/, '')
          return (
            <SyntaxHighlighter
              language={lang}
              style={hlStyles.dark}
              customStyle={{
                margin: 0,
                padding: 0,
                fontSize: 'inherit',
                background: 'transparent',
                borderRadius: 0,
              }}
            >
              {codeStr}
            </SyntaxHighlighter>
          )
        }

        return (
          <code
            className={cn(
              'font-mono',
              compact ? 'text-caption' : 'text-xs',
              isBlock
                ? cn(
                    'bg-transparent whitespace-pre',
                    inverted ? 'text-background' : 'text-foreground',
                  )
                : cn(
                    'rounded px-1 py-px [overflow-wrap:anywhere]',
                    inverted
                      ? 'bg-background/20 text-background'
                      : 'bg-muted text-foreground',
                  ),
            )}
          >
            {children}
          </code>
        )
      },
      table: ({ children }) => (
        <div className={cn('overflow-x-auto', compact ? 'my-1.5' : 'my-4')}>
          <table
            className={cn('w-full border-collapse', compact ? 'text-caption' : 'text-sm')}
          >
            {children}
          </table>
        </div>
      ),
      th: ({ children }) => (
        <th className="border border-border bg-muted px-2 py-1.5 text-left">{children}</th>
      ),
      td: ({ children }) => (
        <td className="border border-border px-2 py-1.5 align-top">{children}</td>
      ),
      hr: () => (
        <hr className={cn('border-none border-t border-border', compact ? 'my-2' : 'my-5')} />
      ),
    }
  }, [compact, inverted, muted, headingAnchors, headingSlugs])

  return (
    <div
      className={cn(
        // overflow-wrap:anywhere, not break-words (break-word): a pasted secret
        // or JWT is a single unbreakable ~2k-char run. break-word wraps the
        // visible line but does NOT reduce the element's min-content width, so
        // the flex message bubble body grows to the token's full width and
        // spills off-screen. `anywhere` counts the break as a min-content
        // opportunity, keeping the body inside the bubble.
        '[overflow-wrap:anywhere]',
        '[&>*:first-child]:mt-0 [&>*:last-child]:mb-0',
        compact ? 'text-xs leading-relaxed' : 'text-sm leading-[1.65]',
        inverted ? 'text-background' : muted ? 'text-muted-foreground' : 'text-foreground',
        className,
      )}
    >
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content || ''}
      </ReactMarkdown>
    </div>
  )
}
