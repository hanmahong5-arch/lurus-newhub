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
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import '@douyinfe/semi-ui/dist/css/semi.css';
import { UserProvider } from './context/User';
import 'react-toastify/dist/ReactToastify.css';
import { StatusProvider } from './context/Status';
import { ThemeProvider } from './context/Theme';
import PageLayout from './components/layout/PageLayout';
import './i18n/i18n';
import './index.css';
import { LocaleProvider } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import zh_CN from '@douyinfe/semi-ui/lib/es/locale/source/zh_CN';
import en_GB from '@douyinfe/semi-ui/lib/es/locale/source/en_GB';
import fr_FR from '@douyinfe/semi-ui/lib/es/locale/source/fr';
import ja_JP from '@douyinfe/semi-ui/lib/es/locale/source/ja_JP';
import ru_RU from '@douyinfe/semi-ui/lib/es/locale/source/ru_RU';
import vi_VN from '@douyinfe/semi-ui/lib/es/locale/source/vi_VN';

// 欢迎信息（二次开发者未经允许不准将此移除）
// Welcome message (Do not remove this without permission from the original developer)
if (typeof window !== 'undefined') {
  console.log(
    '%cWE ❤ AILURUS%c Github: https://github.com/QuantumNous/lurus-api',
    'color: #10b981; font-weight: bold; font-size: 24px;',
    'color: inherit; font-size: 14px;',
  );
}

const SEMI_LOCALES = {
  zh: zh_CN,
  en: en_GB,
  fr: fr_FR,
  ja: ja_JP,
  ru: ru_RU,
  vi: vi_VN,
};

function SemiLocaleWrapper({ children }) {
  const { i18n } = useTranslation();
  // Key on resolvedLanguage, not language. The browser detector reports a full
  // tag ('en-US'), while `load: 'languageOnly'` means i18next resolves that to
  // 'en' for its own lookups. Indexing by the full tag missed every time, so
  // the whole console ran with Chinese Semi widgets — pagination reading
  // 总页数 / 每页条数, Chinese date pickers and empty states — even while the
  // application's own copy rendered in English.
  const semiLocale = React.useMemo(() => {
    const lng = i18n.resolvedLanguage || i18n.language || '';
    return SEMI_LOCALES[lng] || SEMI_LOCALES[lng.split('-')[0]] || zh_CN;
  }, [i18n.resolvedLanguage, i18n.language]);
  return <LocaleProvider locale={semiLocale}>{children}</LocaleProvider>;
}

// initialization

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <React.StrictMode>
    <StatusProvider>
      <UserProvider>
        <BrowserRouter
          future={{
            v7_startTransition: true,
            v7_relativeSplatPath: true,
          }}
        >
          <ThemeProvider>
            <SemiLocaleWrapper>
              <PageLayout />
            </SemiLocaleWrapper>
          </ThemeProvider>
        </BrowserRouter>
      </UserProvider>
    </StatusProvider>
  </React.StrictMode>,
);
