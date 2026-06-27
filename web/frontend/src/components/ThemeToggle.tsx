import { useEffect, useState } from 'react'
import { Moon, Sun } from 'lucide-react'
import { Button } from '@aspect/ui'
import { Tooltip } from './ui/tooltip'

type Theme = 'light' | 'dark'

const storageKey = 'aiscan-theme'

function getInitialTheme(): Theme {
  if (typeof window === 'undefined') {
    return 'dark'
  }
  const storedTheme = window.localStorage.getItem(storageKey)
  return storedTheme === 'light' || storedTheme === 'dark' ? storedTheme : 'dark'
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
  document.documentElement.style.colorScheme = theme
}

export default function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(getInitialTheme)
  const isDark = theme === 'dark'

  useEffect(() => {
    applyTheme(theme)
    window.localStorage.setItem(storageKey, theme)
  }, [theme])

  return (
    <Tooltip content={isDark ? 'Switch to light theme' : 'Switch to dark theme'} side="bottom">
      <Button
        type="button"
        variant="outline"
        size="icon"
        aria-label={isDark ? 'Switch to light theme' : 'Switch to dark theme'}
        onClick={() => setTheme(isDark ? 'light' : 'dark')}
        className="h-10 w-10 shrink-0"
      >
        {isDark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </Button>
    </Tooltip>
  )
}
