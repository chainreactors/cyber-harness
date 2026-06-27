import * as React from 'react'
import { cn } from '@aspect/theme'

interface TooltipProps {
  content: string
  children: React.ReactNode
  side?: 'top' | 'right' | 'bottom' | 'left'
}

function Tooltip({ content, children, side = 'right' }: TooltipProps) {
  const [show, setShow] = React.useState(false)

  const positionClass = {
    top: 'bottom-full left-1/2 -translate-x-1/2 mb-2',
    right: 'left-full top-1/2 -translate-y-1/2 ml-2',
    bottom: 'top-full left-1/2 -translate-x-1/2 mt-2',
    left: 'right-full top-1/2 -translate-y-1/2 mr-2',
  }[side]

  return (
    <div
      className="relative inline-flex"
      onMouseEnter={() => setShow(true)}
      onMouseLeave={() => setShow(false)}
    >
      {children}
      {show && (
        <div
          className={cn(
            'absolute z-50 whitespace-nowrap rounded-md border border-border bg-card px-2.5 py-1.5 text-xs text-card-foreground shadow-lg',
            positionClass
          )}
        >
          {content}
        </div>
      )}
    </div>
  )
}

export { Tooltip }
