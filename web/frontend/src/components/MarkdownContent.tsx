import type { ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Link2 } from 'lucide-react'
import { cn } from '@aspect/theme'

interface MarkdownContentProps {
  content: string
  className?: string
  compact?: boolean
  muted?: boolean
}

export default function MarkdownContent({ content, className, compact = false, muted = false }: MarkdownContentProps) {
  const headingSlugs = new Map<string, number>()

  return (
    <div
      className={cn(
        'prose prose-sm max-w-none break-words dark:prose-invert',
        'prose-headings:font-semibold prose-headings:text-cyber-700 dark:prose-headings:text-cyber-400',
        'prose-h1:border-b prose-h1:border-l-2 prose-h1:border-border prose-h1:border-l-cyber-500 prose-h1:pb-2 prose-h1:pl-3 prose-h1:text-lg',
        'prose-h2:border-l-2 prose-h2:border-l-cyber-500/50 prose-h2:pl-3 prose-h2:text-base',
        'prose-h3:text-sm',
        muted ? 'prose-p:text-muted-foreground prose-li:text-muted-foreground' : 'prose-p:text-foreground/85 prose-li:text-foreground/85',
        'prose-p:leading-relaxed',
        'prose-strong:text-foreground',
        'prose-code:rounded prose-code:bg-secondary prose-code:px-1.5 prose-code:py-0.5 prose-code:text-xs prose-code:text-cyber-700 prose-code:before:content-none prose-code:after:content-none dark:prose-code:text-cyber-300',
        'prose-pre:rounded-lg prose-pre:border prose-pre:border-border prose-pre:bg-secondary',
        'prose-table:text-xs',
        'prose-th:border-border prose-th:bg-secondary/50 prose-th:px-3 prose-th:py-2 prose-th:text-foreground',
        'prose-td:border-border prose-td:px-3 prose-td:py-1.5 prose-td:text-muted-foreground',
        'prose-a:text-cyber-700 prose-a:no-underline hover:prose-a:underline dark:prose-a:text-cyber-400',
        'prose-del:text-red-600 prose-del:opacity-60 dark:prose-del:text-red-400',
        compact && [
          'prose-headings:my-2',
          'prose-h1:border-0 prose-h1:p-0 prose-h1:text-sm',
          'prose-h2:border-0 prose-h2:p-0 prose-h2:text-sm',
          'prose-p:my-1',
          'prose-ul:my-1 prose-ol:my-1',
          'prose-li:my-0',
          'prose-pre:my-2',
          'prose-blockquote:my-2',
          'prose-table:my-2',
        ],
        className,
      )}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: headingComponent('h1', headingSlugs),
          h2: headingComponent('h2', headingSlugs),
          h3: headingComponent('h3', headingSlugs),
          h4: headingComponent('h4', headingSlugs),
          h5: headingComponent('h5', headingSlugs),
          h6: headingComponent('h6', headingSlugs),
          table: ({ children }) => (
            <div className="my-2 overflow-x-auto">
              <table>{children}</table>
            </div>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
}

type HeadingTag = 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

function headingComponent(Tag: HeadingTag, slugCounts: Map<string, number>) {
  return function Heading({ children, ...props }: { children?: ReactNode }) {
    const text = nodeText(children)
    const id = uniqueAnchorSlug(text || 'section', slugCounts)

    return (
      <Tag {...props} id={id} className="group scroll-mt-24">
        {children}
        <a
          href={`#${id}`}
          aria-label={`Link to ${text}`}
          className="ml-2 inline-flex align-middle opacity-0 transition-opacity group-hover:opacity-70 hover:opacity-100"
        >
          <Link2 className="h-3.5 w-3.5" />
        </a>
      </Tag>
    )
  }
}

function nodeText(node: ReactNode): string {
  if (node == null || typeof node === 'boolean') {
    return ''
  }
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node)
  }
  if (Array.isArray(node)) {
    return node.map(nodeText).join('')
  }
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
    .replace(/[^a-z0-9\u4e00-\u9fa5]+/g, '-')
    .replace(/^-+|-+$/g, '')

  return (slug || 'section').slice(0, 96)
}

function uniqueAnchorSlug(value: string, slugCounts: Map<string, number>) {
  const base = anchorSlug(value)
  const count = slugCounts.get(base) || 0
  slugCounts.set(base, count + 1)
  return count === 0 ? base : `${base}-${count + 1}`
}
