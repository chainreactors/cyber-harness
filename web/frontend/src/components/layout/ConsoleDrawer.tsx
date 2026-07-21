import type { ComponentProps, ComponentType, ReactNode } from 'react'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '@cyber/ui'
import { cn } from '@cyber/theme'

type SheetContentProps = ComponentProps<typeof SheetContent>

export interface ConsoleDrawerProps {
  open: boolean
  onClose: () => void
  icon: ComponentType<{ className?: string }>
  title: ReactNode
  description: ReactNode
  titleMeta?: ReactNode
  actions?: ReactNode
  children: ReactNode
  bodyClassName?: string
  contentProps?: Omit<SheetContentProps, 'children' | 'className' | 'side'>
}

export function ConsoleDrawer({
  open,
  onClose,
  icon: Icon,
  title,
  description,
  titleMeta,
  actions,
  children,
  bodyClassName,
  contentProps,
}: ConsoleDrawerProps) {
  return (
    <Sheet open={open} onOpenChange={(next) => { if (!next) onClose() }}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 border-l border-border/70 bg-background p-0 sm:max-w-none md:w-[75vw] md:min-w-[760px] md:max-w-none"
        {...contentProps}
      >
        <header className="flex min-h-14 shrink-0 items-center justify-between gap-4 border-b border-border/70 px-4 pr-12">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-primary/25 bg-primary/10 text-primary">
              <Icon className="h-4 w-4" />
            </div>
            <div className="min-w-0">
              <div className="flex min-w-0 items-center gap-2">
                <SheetTitle className="truncate text-sm font-semibold text-foreground">{title}</SheetTitle>
                {titleMeta}
              </div>
              <SheetDescription className="truncate text-xs text-muted-foreground">
                {description}
              </SheetDescription>
            </div>
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>

        <div className={cn('min-h-0 flex-1 overflow-hidden', bodyClassName)}>
          {children}
        </div>
      </SheetContent>
    </Sheet>
  )
}
