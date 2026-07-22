import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Eye, EyeOff, KeyRound, LoaderCircle } from 'lucide-react'
import { Button, Input, TooltipProvider } from '@cyber/ui'
import { APIError, AUTH_REQUIRED_EVENT, getAuthSession, login } from '../api'
import BrandLogo from './brand/BrandLogo'
import LanguageToggle from './LanguageToggle'

type AuthState = 'checking' | 'authenticated' | 'unauthenticated'

export default function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>('checking')

  useEffect(() => {
    let active = true
    const requireAuth = () => setState('unauthenticated')
    window.addEventListener(AUTH_REQUIRED_EVENT, requireAuth)

    void getAuthSession()
      .then((authenticated) => { if (active) setState(authenticated ? 'authenticated' : 'unauthenticated') })
      .catch(() => { if (active) setState('unauthenticated') })

    return () => {
      active = false
      window.removeEventListener(AUTH_REQUIRED_EVENT, requireAuth)
    }
  }, [])

  if (state === 'checking') return <AuthLoading />
  if (state === 'unauthenticated') {
    return <LoginPage onAuthenticated={() => setState('authenticated')} />
  }
  return children
}

function AuthLoading() {
  const { t } = useTranslation('app')
  return (
    <div className="aspect-theme-root flex min-h-[100dvh] items-center justify-center bg-background text-foreground">
      <div className="flex items-center gap-2 font-mono text-xs text-muted-foreground">
        <LoaderCircle className="h-4 w-4 animate-spin text-primary" aria-hidden="true" />
        <span>{t('authChecking')}</span>
      </div>
    </div>
  )
}

function LoginPage({ onAuthenticated }: { onAuthenticated: () => void }) {
  const { t } = useTranslation('app')
  const [token, setToken] = useState('')
  const [showToken, setShowToken] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = token.trim()
    if (!value) {
      setError(t('loginTokenRequired'))
      return
    }

    setSubmitting(true)
    setError('')
    try {
      await login(value)
      onAuthenticated()
    } catch (err) {
      setError(err instanceof APIError && err.status === 401 ? t('loginInvalid') : t('loginUnavailable'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <TooltipProvider delayDuration={300}>
    <div className="aspect-theme-root relative min-h-[100dvh] overflow-hidden bg-background text-foreground">
      <div className="pointer-events-none absolute inset-0" aria-hidden="true">
        <div className="absolute left-1/2 top-[-18rem] h-[34rem] w-[34rem] -translate-x-1/2 rounded-full bg-primary/[0.08] blur-3xl" />
        <div className="absolute bottom-[-16rem] right-[-10rem] h-[30rem] w-[30rem] rounded-full bg-primary/[0.05] blur-3xl" />
        <div className="absolute inset-0 bg-[linear-gradient(hsl(var(--border)/0.22)_1px,transparent_1px),linear-gradient(90deg,hsl(var(--border)/0.22)_1px,transparent_1px)] bg-[size:48px_48px] [mask-image:linear-gradient(to_bottom,black,transparent_72%)]" />
      </div>

      <header className="relative z-10 flex h-14 items-center justify-between px-5 sm:px-8">
        <div className="flex items-center gap-2.5">
          <BrandLogo size={24} />
          <span className="text-sm font-semibold tracking-tight">AIScan</span>
        </div>
        <LanguageToggle />
      </header>

      <main className="relative z-10 flex min-h-[calc(100dvh-3.5rem)] items-center justify-center px-4 pb-14 sm:px-6">
        <section className="w-full max-w-[400px] rounded-2xl border border-border/80 bg-card/95 p-6 shadow-elevated backdrop-blur sm:p-8">
          <div className="mb-7 flex flex-col items-center text-center">
            <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl border border-primary/20 bg-primary/10 shadow-glow-sm">
              <BrandLogo size={38} />
            </div>
            <h1 className="text-xl font-semibold tracking-tight">{t('loginTitle')}</h1>
            <p className="mt-2 max-w-xs text-sm leading-6 text-muted-foreground">{t('loginDescription')}</p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <input type="text" name="username" value="aiscan" readOnly autoComplete="username" className="hidden" aria-hidden="true" tabIndex={-1} />
            <div className="space-y-2">
              <label htmlFor="access-token" className="text-sm font-medium">{t('loginTokenLabel')}</label>
              <div className="relative">
                <KeyRound className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
                <Input
                  id="access-token"
                  type={showToken ? 'text' : 'password'}
                  value={token}
                  onChange={(event) => {
                    setToken(event.target.value)
                    if (error) setError('')
                  }}
                  autoComplete="current-password"
                  autoFocus
                  spellCheck={false}
                  placeholder={t('loginTokenPlaceholder')}
                  aria-invalid={!!error}
                  aria-describedby={error ? 'login-error' : 'login-hint'}
                  className="h-11 pl-10 pr-10 font-mono"
                />
                <button
                  type="button"
                  onClick={() => setShowToken((visible) => !visible)}
                  className="absolute right-2 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  aria-label={showToken ? t('loginHideToken') : t('loginShowToken')}
                >
                  {showToken ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
              {error ? (
                <p id="login-error" role="alert" className="text-sm text-destructive">{error}</p>
              ) : (
                <p id="login-hint" className="text-xs leading-5 text-muted-foreground">{t('loginSecurityHint')}</p>
              )}
            </div>

            <Button type="submit" className="h-11 w-full" disabled={submitting}>
              {submitting && <LoaderCircle className="mr-2 h-4 w-4 animate-spin" aria-hidden="true" />}
              {submitting ? t('loginSubmitting') : t('loginSubmit')}
            </Button>
          </form>
        </section>
      </main>
    </div>
    </TooltipProvider>
  )
}
