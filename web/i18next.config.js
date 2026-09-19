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

import { defineConfig } from 'i18next-cli';

/** @type {import('i18next-cli').I18nextToolkitConfig} */
export default defineConfig({
  // The languages src/i18n/i18n.js registers (PUBLISHED_LANGUAGES). The first
  // entry is also the extractor's primaryLanguage, and for that one it writes
  // `defaultValue || key` into the file
  // (i18next-cli/dist/esm/extractor/core/translation-manager.js:740) — which is
  // how zh.json came to hold "common.changeLanguage": "common.changeLanguage",
  // i.e. the identifier itself as the aria-label of the language button.
  // src/i18n/locale-coverage.test.js fails on that shape now.
  //
  // fr / ja / ru / vi are no longer registered, so extracting into them writes
  // entries no browser can reach. The files stay in the tree untouched until
  // owner item O-lang decides whether to finish them.
  locales: ['zh', 'en'],
  extract: {
    input: ['src/**/*.{js,jsx,ts,tsx}'],
    ignore: ['src/i18n/**/*'],
    output: 'src/i18n/locales/{{language}}.json',
    ignoredAttributes: [
      'accept',
      'align',
      'aria-label',
      'autoComplete',
      'className',
      'clipRule',
      'color',
      'crossOrigin',
      'data-index',
      'data-name',
      'data-testid',
      'data-type',
      'defaultActiveKey',
      'direction',
      'editorType',
      'field',
      'fill',
      'fillRule',
      'height',
      'hoverStyle',
      'htmlType',
      'id',
      'itemKey',
      'key',
      'keyPrefix',
      'layout',
      'margin',
      'maxHeight',
      'mode',
      'name',
      'overflow',
      'placement',
      'position',
      'rel',
      'role',
      'rowKey',
      'searchPosition',
      'selectedStyle',
      'shape',
      'size',
      'style',
      'theme',
      'trigger',
      'uploadTrigger',
      'validateStatus',
      'value',
      'viewBox',
      'width',
    ],
    sort: true,
    disablePlurals: false,
    removeUnusedKeys: false,
    nsSeparator: false,
    keySeparator: false,
    mergeNamespaces: true,
  },
});
