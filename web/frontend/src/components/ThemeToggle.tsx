import { Moon, Sun } from 'lucide-react'
import { Button } from '@aspect/ui'
import { useTheme } from '@aspect/theme'

export default function ThemeToggle() {
  const { isDark, toggle } = useTheme()

  return (
    <Button
      type="button"
      variant="outline"
      size="icon"
      aria-label={isDark ? 'Switch to light theme' : 'Switch to dark theme'}
      onClick={toggle}
      className="h-10 w-10 shrink-0"
    >
      {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
    </Button>
  )
}
