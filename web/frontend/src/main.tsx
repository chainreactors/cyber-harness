import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './i18n'

import { useTranslation } from 'react-i18next'
import { registerChatExtensions } from './lib/chat-extensions'
import ErrorBoundary from './components/ErrorBoundary'
import AuthGate from './components/AuthGate'
import { ConfirmProvider } from '@cyber/ui'
import './index.css'

registerChatExtensions()

// @cyber/ui's ConfirmDialog is i18n-agnostic (no react-i18next dependency): it
// defaults to English and takes localised strings via `labels`. Inject aiscan's
// translations here so the shared atom speaks the app's language without the
// library having to know about our i18n setup.
function LocalizedConfirmProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation('app')
  return (
    <ConfirmProvider
      labels={{ title: t('confirmTitle'), confirm: t('confirm'), cancel: t('cancel') }}
    >
      {children}
    </ConfirmProvider>
  )
}

declare global {
  interface Window {
    __AISCAN_REACT_ROOT__?: ReturnType<typeof ReactDOM.createRoot>
  }
}

const rootElement = document.getElementById('root')!
const root = window.__AISCAN_REACT_ROOT__ ?? ReactDOM.createRoot(rootElement)
window.__AISCAN_REACT_ROOT__ = root

root.render(
  <React.StrictMode>
    <ErrorBoundary>
      <LocalizedConfirmProvider>
        <AuthGate>
          <App />
        </AuthGate>
      </LocalizedConfirmProvider>
    </ErrorBoundary>
  </React.StrictMode>,
)
