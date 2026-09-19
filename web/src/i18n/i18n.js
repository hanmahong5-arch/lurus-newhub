/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import LanguageDetector from 'i18next-browser-languagedetector';

import enTranslation from './locales/en.json';
import zhTranslation from './locales/zh.json';
import { PUBLISHED_LANGUAGES } from './published';

// Re-exported so that './i18n' stays the one import path a reader reaches for.
// The definitions live in ./published because that module has no imports, and
// callers which only need the list must not pull i18next in with it.
export { PUBLISHED_LANGUAGES, isPublishedLanguage } from './published';

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    load: 'languageOnly',
    resources: {
      en: enTranslation,
      zh: zhTranslation,
    },
    supportedLngs: PUBLISHED_LANGUAGES,
    // Redundant while load: 'languageOnly' is set — i18next 23 ORs the two in
    // LanguageUtils.isSupportedCode (node_modules/i18next/dist/cjs/i18next.js:923)
    // — and kept so that dropping 'languageOnly' cannot silently make zh-CN an
    // unsupported code.
    nonExplicitSupportedLngs: true,
    // Per-language, not a blanket 'en'. A t() key written as its own Chinese
    // source text renders correct Chinese by resolving to itself when zh.json
    // omits it; en.json carries 254 keys zh.json does not (measured on this
    // branch), and a zh -> en fallback would answer those lookups with English
    // for a Chinese operator. An empty array is a fallback list, so
    // getFallbackCodes returns it instead of reaching `default`
    // (i18next.js:952-964: exact code, script part, formatted code, language
    // part, then default — there is no wildcard form).
    fallbackLng: { zh: [], default: ['en'] },
    nsSeparator: false,
    interpolation: {
      escapeValue: false,
    },
  });

export default i18n;
