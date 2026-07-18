import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './i18n'

import { useTranslation } from 'react-i18next'
import { registerChatExtensions } from './lib/chat-extensions'
import ErrorBoundary from './components/ErrorBoundary'
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

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <LocalizedConfirmProvider>
        <App />
      </LocalizedConfirmProvider>
    </ErrorBoundary>
  </React.StrictMode>,
)
