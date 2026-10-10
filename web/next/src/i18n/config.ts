import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

import {
  readStoredLanguage,
  storeLanguage,
  normalizeLanguage,
} from './languages'
import { buildResources } from './resources'

function syncDocumentLanguage(code: string) {
  if (typeof document !== 'undefined') {
    document.documentElement.lang = code === 'en' ? 'en' : 'zh-CN'
  }
}

void i18n.use(initReactI18next).init({
  resources: buildResources(),
  lng: readStoredLanguage(),
  fallbackLng: 'en',
  supportedLngs: ['zh', 'en'],
  load: 'currentOnly',
  nsSeparator: false, // keys are English sentences and may contain colons
  keySeparator: false, // ...and periods
  interpolation: { escapeValue: false },
})

syncDocumentLanguage(i18n.language)
i18n.on('languageChanged', (code) => {
  const normalized = normalizeLanguage(code)
  storeLanguage(normalized)
  syncDocumentLanguage(normalized)
})

export default i18n
