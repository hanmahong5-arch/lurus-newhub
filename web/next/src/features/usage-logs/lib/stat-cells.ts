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
