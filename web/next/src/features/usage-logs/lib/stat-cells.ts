/*
Copyright (C) 2023-2026 QuantumNous

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
import { currentLocale } from '@/lib/locale'
import { formatQuota, type MoneyConfig } from '@/lib/money'

import { compactNumber, type LogStat } from './logs'

const PLACEHOLDER = '--'

/** Pure: the cells the header renders, as [label key, value, hint key]. */
export function statCells(
  stat: LogStat | undefined,
  money: MoneyConfig | null
): Array<{ id: string; label: string; value: string; hint: string }> {
  const num = (n: number | undefined) =>
    stat ? Number(n ?? 0).toLocaleString(currentLocale()) : PLACEHOLDER
  return [
    {
      id: 'requests',
      label: 'Requests',
      value: num(stat?.total_requests),
      hint: 'In window',
    },
    {
      id: 'quota',
      label: 'Spend',
      value: stat ? formatQuota(stat.total_quota, money) : PLACEHOLDER,
      hint: 'In window',
    },
    {
      id: 'tokens',
      label: 'Tokens in / out',
      value: stat
        ? `${compactNumber(stat.prompt_tokens)} / ${compactNumber(stat.completion_tokens)}`
        : PLACEHOLDER,
      hint: 'In window',
    },
    {
      id: 'cache',
      label: 'Cache reads',
      value: stat ? compactNumber(stat.cache_read_tokens) : PLACEHOLDER,
      hint: 'Tokens served from cache',
    },
    {
      id: 'rpm',
      label: 'RPM',
      value: num(stat?.rpm),
      hint: 'Last 60s',
    },
    {
      id: 'tpm',
      label: 'TPM',
      value: num(stat?.tpm),
      hint: 'Last 60s',
    },
  ]
}
