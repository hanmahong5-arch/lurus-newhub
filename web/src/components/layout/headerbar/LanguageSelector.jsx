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

import React from 'react';
import { Button, Dropdown } from '@douyinfe/semi-ui';
import { Languages } from 'lucide-react';
import { CN, GB } from 'country-flag-icons/react/3x2';
import { useTranslation } from 'react-i18next';

const languagePart = (code) =>
  String(code || '')
    .split('-')[0]
    .toLowerCase();

const LanguageSelector = ({ currentLang, onLanguageChange, t }) => {
  const { i18n } = useTranslation();
  /*
   * Which entry is marked active follows the language being rendered, not the
   * tag the header bar happens to hold.
   *
   * useHeaderBar passes i18n.language, and for every mainstream browser that
   * is a region tag: a zh-CN browser lands on 'zh-CN' and an en-US browser on
   * 'en-US' (getBestMatchFromCodes keeps a supported region tag as it stands;
   * pinned in src/i18n/locale-coverage.test.js). Comparing that against 'zh'
   * and 'en' matched neither, so the menu marked nothing until the operator
   * picked a language by hand. resolvedLanguage is the bundle actually in use
   * — 'zh' / 'en' — and it is also right in the one case the two disagree in
   * substance: an explicit changeLanguage('ja') leaves language on 'ja' while
   * the screen renders English, and English is what should be marked.
   *
   * currentLang stays the fallback for a render with no i18next instance in
   * context.
   */
  const activeLang = languagePart(i18n?.resolvedLanguage || currentLang);
  return (
    <Dropdown
      position='bottomRight'
      render={
        <Dropdown.Menu className='!bg-semi-color-bg-overlay !border-semi-color-border !shadow-lg !rounded-lg dark:!bg-gray-700 dark:!border-gray-600'>
          {/* One entry per language i18n.js registers, ordered by English name.
              fr / ja / ru / vi were offered here while their bundles covered
              58% of en.json, so picking one produced a part-Chinese console;
              locale-coverage.test.js compares this list against the registered
              set, in both directions. */}
          <Dropdown.Item
            onClick={() => onLanguageChange('zh')}
            className={`!flex !items-center !gap-2 !px-3 !py-1.5 !text-sm !text-semi-color-text-0 dark:!text-gray-200 ${activeLang === 'zh' ? '!bg-semi-color-primary-light-default dark:!bg-blue-600 !font-semibold' : 'hover:!bg-semi-color-fill-1 dark:hover:!bg-gray-600'}`}
          >
            <CN title='中文' className='!w-5 !h-auto' />
            <span>中文</span>
          </Dropdown.Item>
          <Dropdown.Item
            onClick={() => onLanguageChange('en')}
            className={`!flex !items-center !gap-2 !px-3 !py-1.5 !text-sm !text-semi-color-text-0 dark:!text-gray-200 ${activeLang === 'en' ? '!bg-semi-color-primary-light-default dark:!bg-blue-600 !font-semibold' : 'hover:!bg-semi-color-fill-1 dark:hover:!bg-gray-600'}`}
          >
            <GB title='English' className='!w-5 !h-auto' />
            <span>English</span>
          </Dropdown.Item>
        </Dropdown.Menu>
      }
    >
      <Button
        icon={<Languages size={18} />}
        aria-label={t('common.changeLanguage')}
        theme='borderless'
        type='tertiary'
        className='!p-1.5 !text-current focus:!bg-semi-color-fill-1 dark:focus:!bg-gray-700 !rounded-full !bg-semi-color-fill-0 dark:!bg-semi-color-fill-1 hover:!bg-semi-color-fill-1 dark:hover:!bg-semi-color-fill-2'
      />
    </Dropdown>
  );
};

export default LanguageSelector;
