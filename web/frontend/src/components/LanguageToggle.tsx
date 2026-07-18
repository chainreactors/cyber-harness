import { useTranslation } from 'react-i18next'
import { Button, Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { cn } from '@cyber/theme'

interface LanguageToggleProps {
  className?: string
}

// Toggles the UI language between Chinese and English. Mirrors the icon-button
// styling used by the header actions next to ThemeToggle.
export default function LanguageToggle({ className }: LanguageToggleProps) {
  const { i18n } = useTranslation()
  const isZh = (i18n.resolvedLanguage || i18n.language || 'en').toLowerCase().startsWith('zh')
  const next = isZh ? 'en' : 'zh'

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={() => void i18n.changeLanguage(next)}
          aria-label={isZh ? 'Switch to English' : '切换到中文'}
          className={cn('text-[11px] font-semibold text-muted-foreground', className)}
        >
          {isZh ? 'EN' : '中'}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{isZh ? 'English' : '中文'}</TooltipContent>
    </Tooltip>
  )
}
