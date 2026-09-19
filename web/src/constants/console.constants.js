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

import dayjs from 'dayjs';

// ========== 日期预设常量 ==========
/*
 * `text` is an i18n key, not a label: both consumers render it as
 * t(preset.text) (components/table/task-logs/TaskLogsFilters.jsx and
 * components/table/mj-logs/MjLogsFilters.jsx, both on routed pages —
 * /console/task and /console/midjourney). A key the bundle does not carry
 * resolves to itself, so until en.json was given these five the English
 * console showed Chinese quick-range buttons: the scan in
 * src/i18n/i18n-integrity.test.js reads literal t('…') arguments and cannot
 * see a key that arrives through a variable.
 *
 * Adding a sixth preset therefore means adding its English to
 * src/i18n/locales/en.json. The case named 'every key table handed to t()
 * through a variable resolves in en.json' in that file fails if it is not.
 */
export const DATE_RANGE_PRESETS = [
  {
    text: '今天',
    start: () => dayjs().startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: '近 7 天',
    start: () => dayjs().subtract(6, 'day').startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: '本周',
    start: () => dayjs().startOf('week').toDate(),
    end: () => dayjs().endOf('week').toDate(),
  },
  {
    text: '近 30 天',
    start: () => dayjs().subtract(29, 'day').startOf('day').toDate(),
    end: () => dayjs().endOf('day').toDate(),
  },
  {
    text: '本月',
    start: () => dayjs().startOf('month').toDate(),
    end: () => dayjs().endOf('month').toDate(),
  },
];
