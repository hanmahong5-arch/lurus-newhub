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

/*
 * The set of languages this build ships, with no imports and no side effects.
 *
 * It is separate from i18n.js so that a caller which only needs to ask
 * "is this tag one we ship?" does not pull i18next, the browser language
 * detector and two locale bundles into its module graph — importing i18n.js
 * from PageLayout.jsx broke that page's suite outright, because loading it
 * runs i18n.init() against whatever react-i18next mock the suite installed.
 *
 * i18n.js re-exports both names, so `import { PUBLISHED_LANGUAGES } from
 * './i18n'` keeps working; src/i18n/locale-coverage.test.js checks the list
 * against the bundles i18next actually registered.
 */

/**
 * fr / ja / ru / vi still live in src/i18n/locales and carry 58% of en.json's
 * keys, which is why they are not here: registering them let the browser
 * language detector land an operator on a console that was two fifths Chinese.
 * Finishing them is owner item O-lang.
 */
export const PUBLISHED_LANGUAGES = ['zh', 'en'];

/**
 * True when i18next would keep this tag rather than resolve it away.
 *
 * Region subtags are accepted because i18n.js runs with load: 'languageOnly',
 * so 'zh-CN' and 'en-US' are shipped languages as far as the runtime is
 * concerned. Callers use this before replaying a stored tag: handing i18next a
 * tag it does not ship leaves i18n.language on that tag while the screen
 * renders the fallback, and the language detector then writes the unshipped tag
 * straight back to localStorage — which is how one ?lng=ja visit stuck.
 */
export const isPublishedLanguage = (code) =>
  typeof code === 'string' &&
  PUBLISHED_LANGUAGES.includes(code.split('-')[0].toLowerCase());
