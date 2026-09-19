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

/**
 * The languages this build ships.
 *
 * fr / ja / ru / vi still live in ./locales and carry 58% of en.json's keys
 * (measured: fr 58.13, ja 57.98, ru 58.13, vi 58.20). Registering them let the
 * browser language detector land an operator on a console that was two fifths
 * Chinese, so they are not here — the files stay in the tree, which makes this
 * reversible. Finishing one to the floor in
 * src/i18n/locale-coverage.test.js is what puts it back (owner item O-lang).
 */
export const PUBLISHED_LANGUAGES = ['zh', 'en'];

/**
 * Keep <html lang> on the language the operator is actually reading.
 *
 * index.html ships lang="en" for the pre-JavaScript document; from the moment
 * i18next resolves a language this follows it. resolvedLanguage, not language:
 * an explicit changeLanguage('ja') leaves language on 'ja' while every string
 * resolves through the fallback, and declaring a document Japanese while it
 * renders English is the same lie in a different place — the browser offers to
 * machine-translate an already-English page and a screen reader picks the
 * wrong voice.
 */
const syncDocumentLanguage = (lng) => {
  if (typeof document === 'undefined') return;
  document.documentElement.lang = i18n.resolvedLanguage || lng || 'en';
};

// Registered before init: i18next emits languageChanged from inside init's own
// changeLanguage (i18next.js changeLanguage -> done), and setResolvedLanguage
// runs before that emit, so the first call already sees the resolved tag.
i18n.on('languageChanged', syncDocumentLanguage);

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
    // omits it; en.json carries 265 keys zh.json does not (measured on this
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
