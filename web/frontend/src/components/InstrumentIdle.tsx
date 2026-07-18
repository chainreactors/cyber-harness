import type { ReactNode } from 'react'
import { cn } from '@cyber/theme'
import BrandLogo from './brand/BrandLogo'

interface InstrumentIdleProps {
  /** mono eyebrow, e.g. "AWAITING TARGET" */
  eyebrow: string
  title: string
  subtitle?: ReactNode
  /** metric pills / readouts rendered under the copy */
  children?: ReactNode
  /** glyph inside the frame; defaults to the full-colour brand BrandLogo */
  glyph?: ReactNode
  /** show the travelling scanline (an instrument actively listening) */
  live?: boolean
  className?: string
}

/**
 * An instrument at rest, not a blank void. A reticle-framed measurement field
 * (tactical grid + optional scanline) cradling the scope glyph, with a mono
 * "readout" eyebrow. The shared idle/empty state across the app.
 */
export default function InstrumentIdle({
  eyebrow,
  title,
  subtitle,
  children,
  glyph,
  live = true,
  className,
}: InstrumentIdleProps) {
  return (
    <div className={cn('flex min-h-0 flex-1 items-center justify-center p-6', className)}>
      <div className="flex w-full max-w-sm flex-col items-center text-center animate-in fade-in duration-500">
        {/* instrument frame */}
        <div className="reticle relative mb-6 flex h-36 w-36 items-center justify-center overflow-hidden rounded-2xl border border-border/60 bg-card/30 shadow-soft">
          <div className="absolute inset-0 grid-tactical opacity-70" />
          {/* concentric range rings */}
          <div className="absolute h-28 w-28 rounded-full border border-primary/10" />
          <div className="absolute h-20 w-20 rounded-full border border-primary/10" />
          {live && <div className="scanline absolute inset-x-0 inset-y-0" />}
          {/* the glyph is the brand identity → red logo + red glow, while the
              reticle frame / rings / scanline stay --primary blue (system scope) */}
          <div className="relative drop-shadow-[0_0_10px_rgba(255,77,77,0.45)]">
            {glyph ?? <BrandLogo size={52} />}
          </div>
        </div>

        <div className="mono-label flex items-center gap-1.5 text-primary/80">
          <span>{eyebrow}</span>
          <span className="blink text-primary">▮</span>
        </div>
        <h2 className="mt-2 font-display text-lg font-semibold tracking-tight text-foreground">{title}</h2>
        {subtitle && <p className="mt-1 max-w-xs text-xs leading-relaxed text-muted-foreground">{subtitle}</p>}

        {children && <div className="mt-5 flex flex-wrap justify-center gap-2">{children}</div>}
      </div>
    </div>
  )
}
