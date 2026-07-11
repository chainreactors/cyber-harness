import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { Button } from '@aspect/ui'

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

/**
 * Top-level crash guard. The deck streams live scan/agent data and renders
 * server-shaped results, so a single malformed record or null deref would
 * otherwise white-screen the whole app with no way back. Catch it and offer a
 * recoverable fallback (retry the render, or hard-reload) instead of a blank page.
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Keep the stack in the console for debugging; the fallback speaks to the user.
    console.error('UI crashed:', error, info.componentStack)
  }

  reset = () => this.setState({ error: null })

  render() {
    if (this.state.error) {
      return <ErrorFallback error={this.state.error} onReset={this.reset} />
    }
    return this.props.children
  }
}

function ErrorFallback({ error, onReset }: { error: Error; onReset: () => void }) {
  const { t } = useTranslation('app')
  return (
    <div className="flex h-[100dvh] flex-col items-center justify-center gap-4 bg-background p-6 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-2xl border border-destructive/30 bg-destructive/10 text-destructive">
        <AlertTriangle className="h-7 w-7" />
      </div>
      <div className="space-y-1">
        <h1 className="text-lg font-semibold text-foreground">{t('crashTitle', 'Something went wrong')}</h1>
        <p className="max-w-md text-sm text-muted-foreground">
          {t('crashHint', 'The interface hit an unexpected error. Reloading usually clears it.')}
        </p>
      </div>
      {error.message && (
        <pre className="max-w-md overflow-auto rounded-md border border-border bg-card px-3 py-2 text-left font-mono text-[11px] text-muted-foreground">
          {error.message}
        </pre>
      )}
      <div className="flex gap-2">
        <Button
          type="button"
          variant="secondary"
          onClick={onReset}
          className="border border-border text-foreground hover:bg-accent"
        >
          {t('crashReset', 'Try again')}
        </Button>
        <Button type="button" onClick={() => window.location.reload()}>
          {t('crashReload', 'Reload')}
        </Button>
      </div>
    </div>
  )
}
