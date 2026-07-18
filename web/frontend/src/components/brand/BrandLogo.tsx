import { useId } from 'react'

interface BrandLogoProps {
  /** pixel size of the square mark */
  size?: number
  className?: string
  /** rotate the radar sweep + pulse the core (default on) */
  animated?: boolean
}

// Eight agent nodes evenly spaced on the chain ring (R = 12.5, from 12 o'clock,
// clockwise). Hard-coded to keep the glyph a pure literal like the source logo.
const NODES: Array<[number, number]> = [
  [16, 3.5], [24.84, 7.16], [28.5, 16], [24.84, 24.84],
  [16, 28.5], [7.16, 24.84], [3.5, 16], [7.16, 7.16],
]

/**
 * The AIScan brand logo — the official "RADAR-CHAIN" mark: a range-ring radar
 * with a live sweep, ringed by eight chain-linked agent nodes (the chainreactors
 * chain). Full brand colour (crimson → red) and animated; this is the headline
 * mark. For small monochrome/tintable glyphs (chat markers, idle hero) use
 * BrandMark instead. Mirrors assets/logo.svg. See [[aiscan-web-redesign-direction]].
 */
export default function BrandLogo({ size = 28, className, animated = true }: BrandLogoProps) {
  // Unique gradient ids per instance so multiple logos on a page never cross-wire
  // their url(#…) fills (colons are illegal in fragment refs, so strip them).
  const uid = useId().replace(/:/g, '')
  const gr = `${uid}-gr`
  const sweep = `${uid}-sweep`
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true" className={className}>
      <defs>
        <linearGradient id={gr} x1="0" y1="32" x2="32" y2="0" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#800000" />
          <stop offset="1" stopColor="#ff4d4d" />
        </linearGradient>
        {/* radially symmetric about the core, so it stays rim-bright as it spins */}
        <radialGradient id={sweep} cx="16" cy="16" r="12.5" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#ff4d4d" stopOpacity="0.04" />
          <stop offset="1" stopColor="#ff4d4d" stopOpacity="0.5" />
        </radialGradient>
      </defs>

      {/* chain ring — the loop the agent nodes ride */}
      <circle cx="16" cy="16" r="12.5" fill="none" stroke={`url(#${gr})`} strokeWidth="1" opacity="0.3" strokeDasharray="1.5 3" />

      {/* radar range rings */}
      <circle cx="16" cy="16" r="8.5" fill="none" stroke={`url(#${gr})`} strokeWidth="1" opacity="0.35" />
      <circle cx="16" cy="16" r="5" fill="none" stroke={`url(#${gr})`} strokeWidth="1.1" opacity="0.6" />

      {/* crosshair */}
      <g stroke={`url(#${gr})`} strokeWidth="0.8" opacity="0.35">
        <line x1="16" y1="6.5" x2="16" y2="25.5" />
        <line x1="6.5" y1="16" x2="25.5" y2="16" />
      </g>

      {/* live radar sweep — a wedge with a bright leading beam, rotating clockwise */}
      <g>
        <path d="M16 16 L16 3.5 A12.5 12.5 0 0 1 24.84 7.16 Z" fill={`url(#${sweep})`} />
        <line x1="16" y1="16" x2="16" y2="3.5" stroke="#ff4d4d" strokeWidth="0.9" strokeLinecap="round" opacity="0.85" />
        {animated && (
          <animateTransform attributeName="transform" type="rotate" from="0 16 16" to="360 16 16" dur="4s" repeatCount="indefinite" />
        )}
      </g>

      {/* chain nodes riding the ring */}
      {NODES.map(([cx, cy], i) => (
        <g key={i}>
          <circle cx={cx} cy={cy} r="1.5" fill="#d90429" fillOpacity="0.85" stroke={`url(#${gr})`} strokeWidth="1" />
          <circle cx={cx} cy={cy} r="0.5" fill="#fff" />
        </g>
      ))}

      {/* core — a white nucleus that gently breathes, red pinpoint on top */}
      <circle cx="16" cy="16" r="2.2" fill="#fff" opacity="0.9">
        {animated && <animate attributeName="opacity" values="0.55;0.95;0.55" dur="2.4s" repeatCount="indefinite" />}
      </circle>
      <circle cx="16" cy="16" r="1.1" fill="#ff4d4d" />
    </svg>
  )
}
