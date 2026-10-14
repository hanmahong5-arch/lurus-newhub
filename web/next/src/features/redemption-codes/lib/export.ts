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
export type ExportFormat = 'txt' | 'md'

export interface ExportInput {
  keys: string[]
  name: string
  /** Quota already formatted as money, e.g. "$10.00". */
  quota: string
  format: ExportFormat
  includeName: boolean
  includeQuota: boolean
  labels: { name: string; code: string; quota: string }
}

function flatten(value: string): string {
  return value.replaceAll(/[\t\r\n]+/g, ' ')
}

/**
 * Text of the downloadable file: tab-separated rows for TXT, a pipe table
 * (with Markdown-significant characters escaped) for MD.
 */
export function buildExportContent(input: ExportInput): string {
  const headers: string[] = []
  if (input.includeName) headers.push(input.labels.name)
  headers.push(input.labels.code)
  if (input.includeQuota) headers.push(input.labels.quota)

  const rows = input.keys.map((key) => {
    const row: string[] = []
    if (input.includeName) row.push(input.name)
    row.push(key)
    if (input.includeQuota) row.push(input.quota)
    return row.map(flatten)
  })

  if (input.format === 'txt') {
    return `${rows.map((row) => row.join('\t')).join('\n')}\n`
  }

  const lines = [headers, headers.map(() => '---'), ...rows].map(
    (row, index) => {
      const cells =
        index === 1
          ? row
          : row.map((value) =>
              value
                .replaceAll('&', '&amp;')
                .replaceAll(/[\\`*_[\]|<>]/g, '\\$&')
            )
      return `| ${cells.join(' | ')} |`
    }
  )
  return `${lines.join('\n')}\n`
}
