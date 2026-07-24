import { Badge } from '@cyber/ui'
import { cn } from '@cyber/theme'
import {
  GraphPanel as CyberGraphPanel,
  MessageContent as CyberMessageContent,
  type GraphPanelProps as CyberGraphPanelProps,
  type MessageContentProps as CyberMessageContentProps,
} from '../../cyber-ui/packages/ioa/src'

export * from '../../cyber-ui/packages/ioa/src'

export interface GraphPanelProps extends CyberGraphPanelProps {
  title?: string
}

export function GraphPanel({ title: _title, ...props }: GraphPanelProps) {
  return <CyberGraphPanel {...props} />
}

export interface MessageContentProps extends CyberMessageContentProps {
  showFrontMatter?: boolean
  showType?: boolean
}

export function MessageContent({
  showFrontMatter: _showFrontMatter,
  showType: _showType,
  ...props
}: MessageContentProps) {
  return <CyberMessageContent {...props} />
}

export interface MessageFrontMatterProps {
  content: unknown
  meta?: Record<string, unknown>
  className?: string
}

export function MessageFrontMatter({ content, meta, className }: MessageFrontMatterProps) {
  const record = content && typeof content === 'object' && !Array.isArray(content)
    ? content as Record<string, unknown>
    : null
  const contentType = typeof record?.type === 'string' ? record.type : ''
  const metaKind = typeof meta?.kind === 'string' ? meta.kind : ''
  const metaLabels = Array.isArray(meta?.labels)
    ? meta.labels.filter((label): label is string => typeof label === 'string')
    : []

  if (!contentType && !metaKind && metaLabels.length === 0) return null

  return (
    <div className={cn('flex min-w-0 flex-wrap items-center gap-1.5', className)}>
      {contentType && (
        <Badge variant="outline" className="rounded-md px-1.5 py-px text-[10px]">
          {contentType}
        </Badge>
      )}
      {metaKind && (
        <Badge variant="secondary" className="rounded-md px-1.5 py-px text-[10px]">
          {metaKind}
        </Badge>
      )}
      {metaLabels.map(label => (
        <Badge key={label} variant="secondary" className="rounded-md px-1.5 py-px text-[10px]">
          {label}
        </Badge>
      ))}
    </div>
  )
}
