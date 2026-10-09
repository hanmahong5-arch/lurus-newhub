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
import { tenantApi } from '@/lib/api';

import type { BatchItemBody, IssuedRow } from './lib/roster';

export interface SkippedRow {
  employee_ref: string;
  id: number;
  name: string;
}

export interface BatchResult {
  created: IssuedRow[];
  skipped: SkippedRow[];
  requested: number;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null;
}

/** Defensive mapping of POST /tokens/batch; a malformed body throws. */
export function mapBatchResult(raw: unknown): BatchResult {
  if (!isRecord(raw)) throw new Error('Unexpected response from server');
  const list = (v: unknown) => (Array.isArray(v) ? v.filter(isRecord) : []);
  return {
    created: list(raw.created).map((c) => ({
      employee_ref: String(c.employee_ref ?? ''),
      name: String(c.name ?? ''),
      key: String(c.key ?? ''),
    })),
    skipped: list(raw.skipped).map((s) => ({
      employee_ref: String(s.employee_ref ?? ''),
      id: Number(s.id ?? 0),
      name: String(s.name ?? ''),
    })),
    requested: Number(raw.requested ?? 0),
  };
}

/**
 * The endpoint takes no Idempotency-Key: it is idempotent per employee_ref
 * (an employee with a live key lands in `skipped`), so a retry is safe.
 */
export async function batchCreateTokens(
  items: BatchItemBody[],
): Promise<BatchResult> {
  return mapBatchResult(
    await tenantApi.post('/tokens/batch', { tokens: items }),
  );
}
