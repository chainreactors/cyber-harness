import { cn } from '@cyber/theme'

interface BrandMarkProps {
  /** pixel size of the square mark */
  size?: number
  className?: string
}

// Eight agent nodes on the ring (R = 12, from 12 o'clock, clockwise).
const NODES: Array<[number, number]> = [
  [16, 4], [24.5, 7.5], [28, 16], [24.5, 24.5],
  [16, 28], [7.5, 24.5], [4, 16], [7.5, 7.5],
]

/**
 * The AIScan scope glyph — a monochrome take on the official radar-and-chain
 * mark (web/assets/logo.svg): a range-ring radar ringed by eight agent nodes. Drawn
 * in a single tint via currentColor so callers colour it with text-* utilities
 * (e.g. text-ai, text-primary, text-muted-foreground). The full-colour, animated
 * brand logo lives in [[BrandLogo]].
 */
export default function BrandMark({ size = 28, className }: BrandMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      aria-hidden="true"
      className={cn('text-primary', className)}
    >
      {/* node ring */}
      <circle cx="16" cy="16" r="12" fill="none" stroke="currentColor" strokeWidth={1.2} opacity={0.5} />
      {NODES.map(([cx, cy], i) => (
        <circle key={i} cx={cx} cy={cy} r={1.25} fill="currentColor" opacity={0.9} />
      ))}
      {/* inner range ring */}
      <circle cx="16" cy="16" r="6.2" fill="none" stroke="currentColor" strokeWidth={1.2} opacity={0.75} />
      {/* crosshair */}
      <g stroke="currentColor" strokeWidth={1} opacity={0.5}>
        <line x1="16" y1="7.5" x2="16" y2="24.5" />
        <line x1="7.5" y1="16" x2="24.5" y2="16" />
      </g>
      {/* core */}
      <circle cx="16" cy="16" r="2.3" fill="currentColor" />
    </svg>
  )
}
