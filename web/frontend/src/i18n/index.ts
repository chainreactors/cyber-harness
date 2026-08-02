import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import enApp from './locales/en/app'
import zhApp from './locales/zh/app'
import enSidebar from './locales/en/sidebar'
import zhSidebar from './locales/zh/sidebar'
import enScan from './locales/en/scan'
import zhScan from './locales/zh/scan'
import enFindings from './locales/en/findings'
import zhFindings from './locales/zh/findings'
import enChat from './locales/en/chat'
import zhChat from './locales/zh/chat'
import enAgent from './locales/en/agent'
import zhAgent from './locales/zh/agent'
import enConfig from './locales/en/config'
import zhConfig from './locales/zh/config'
import enAssets from './locales/en/assets'
import zhAssets from './locales/zh/assets'
import enIOA from './locales/en/ioa'
import zhIOA from './locales/zh/ioa'
import enTools from './locales/en/tools'
import zhTools from './locales/zh/tools'

export const STORAGE_KEY = 'aiscan-locale'
export const SUPPORTED_LOCALES = ['en', 'zh'] as const
export type Locale = (typeof SUPPORTED_LOCALES)[number]
export const defaultNS = 'app'

export const resources = {
  en: {
    app: enApp,
    sidebar: enSidebar,
    scan: enScan,
    findings: enFindings,
    chat: enChat,
    agent: enAgent,
    config: enConfig,
    assets: enAssets,
    ioa: enIOA,
    tools: enTools,
  },
  zh: {
    app: zhApp,
    sidebar: zhSidebar,
    scan: zhScan,
    findings: zhFindings,
    chat: zhChat,
    agent: zhAgent,
    config: zhConfig,
    assets: zhAssets,
    ioa: zhIOA,
    tools: zhTools,
  },
} as const

// Keep <html lang> in sync with the active locale so CSS `:lang(zh)` rules
// (CJK letter-spacing / typographic tuning) apply, and so the UA picks the
// correct CJK glyph forms. index.html hard-codes lang="en" at boot.
const applyDocumentLang = (lng?: string) => {
  if (typeof document === 'undefined') return
  document.documentElement.lang = (lng || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en'
}
i18n.on('languageChanged', applyDocumentLang)

void i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: SUPPORTED_LOCALES as unknown as string[],
    nonExplicitSupportedLngs: true,
    ns: ['app', 'sidebar', 'scan', 'findings', 'chat', 'agent', 'config', 'assets', 'ioa', 'tools'],
    defaultNS,
    interpolation: { escapeValue: false },
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: STORAGE_KEY,
      caches: ['localStorage'],
    },
  })
  .then(() => applyDocumentLang(i18n.resolvedLanguage || i18n.language))

export default i18n
