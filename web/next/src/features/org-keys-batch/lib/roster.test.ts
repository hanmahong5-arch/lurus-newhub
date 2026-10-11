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
import { describe, expect, it } from 'vitest';

import type { MoneyConfig } from '@/lib/money';

import {
  buildItems,
  issuedToCsv,
  parseRoster,
  validateRows,
  type RosterRow,
} from './roster';

const USD: MoneyConfig = {
  quotaPerUnit: 500000,
  displayType: 'USD',
  symbol: '$',
  rate: 1,
};

function rowsOf(text: string): RosterRow[] {
  const p = parseRoster(text);
  if (!p.ok) throw new Error(p.reason);
  return p.rows;
}

describe('parseRoster', () => {
  it('maps columns, handles quotes, CRLF and BOM', () => {
    const rows = rowsOf(
      '﻿employee_ref,name,quota\r\nE1,"Smith, A",5\r\nE2,,\r\n',
    );
    expect(rows).toHaveLength(2);
    expect(rows[0]).toMatchObject({
      employeeRef: 'E1',
      name: 'Smith, A',
      quotaText: '5',
    });
    expect(rows[1]).toMatchObject({
      employeeRef: 'E2',
      name: '',
      quotaText: '',
    });
  });
  it('requires the employee_ref header and data rows', () => {
    expect(parseRoster('name\nA')).toEqual({
      ok: false,
      reason: 'no_employee_ref_column',
    });
    expect(parseRoster('employee_ref\n')).toEqual({
      ok: false,
      reason: 'empty',
    });
    expect(parseRoster('  \n')).toEqual({ ok: false, reason: 'empty' });
  });
  it('rejects more than 500 rows', () => {
    const body = Array.from({ length: 501 }, (_, i) => `E${i}`).join('\n');
    expect(parseRoster(`employee_ref\n${body}`)).toEqual({
      ok: false,
      reason: 'too_many_rows',
      count: 501,
    });
  });
  it('accepts exactly 500 rows', () => {
    const body = Array.from({ length: 500 }, (_, i) => `E${i}`).join('\n');
    expect(parseRoster(`employee_ref\n${body}`).ok).toBe(true);
  });
});

describe('validateRows', () => {
  it('flags bad refs, duplicates, long names and bad quota', () => {
    const rows = validateRows(
      rowsOf(
        `employee_ref,name,quota\nok.1,,\nbad ref,,\nok.1,,\n,Nobody,\nE4,${'x'.repeat(51)},\nE5,,abc\nE6,,0`,
      ),
      USD,
    );
    expect(rows.map((r) => r.issues)).toEqual([
      [],
      ['ref_invalid'],
      ['ref_duplicate'],
      ['ref_required'],
      ['name_too_long'],
      ['quota_invalid'],
      ['quota_invalid'],
    ]);
  });
  it('rejects a 65 char ref and accepts 64', () => {
    const rows = validateRows(
      rowsOf(`employee_ref,name\n${'a'.repeat(64)},n\n${'a'.repeat(65)},n`),
      USD,
    );
    expect(rows[0].issues).toEqual([]);
    expect(rows[1].issues).toEqual(['ref_invalid']);
  });
  it('converts display money to quota through the unit price', () => {
    const [r] = validateRows(rowsOf('employee_ref,quota\nE1,2'), USD);
    expect(r.quota).toBe(1000000);
  });
  it('quota cannot be converted without a unit price', () => {
    const [r] = validateRows(rowsOf('employee_ref,quota\nE1,2'), null);
    expect(r.issues).toEqual(['quota_unavailable']);
  });
});

describe('buildItems', () => {
  it('blank quota is unlimited; name defaults to ref; dept is not sent', () => {
    const rows = validateRows(
      rowsOf(
        'employee_ref,name,dept_external_code,quota\nE1,,D1,\nE2,Bob,D2,1',
      ),
      USD,
    );
    expect(buildItems(rows)).toEqual([
      {
        name: 'E1',
        employee_ref: 'E1',
        unlimited_quota: true,
        remain_quota: 0,
      },
      {
        name: 'Bob',
        employee_ref: 'E2',
        unlimited_quota: false,
        remain_quota: 500000,
      },
    ]);
  });
});

describe('issuedToCsv', () => {
  it('quotes commas and neutralises formula cells', () => {
    const csv = issuedToCsv([
      { employee_ref: 'E1', name: '=cmd, x', key: 'sk-abc' },
    ]);
    expect(csv).toBe('employee_ref,name,key\r\nE1,"\'=cmd, x",sk-abc\r\n');
  });
});

describe('validateRows backend parity', () => {
  it('counts name length in UTF-8 bytes like the backend', () => {
    const ok = '汉'.repeat(16); // 48 bytes
    const bad = '汉'.repeat(17); // 51 bytes
    const rows = validateRows(
      rowsOf(`employee_ref,name\nE1,${ok}\nE2,${bad}`),
      USD,
    );
    expect(rows[0].issues).toEqual([]);
    expect(rows[1].issues).toEqual(['name_too_long']);
  });
  it('a blank name falls back to the ref, which is then length-checked', () => {
    const rows = validateRows(rowsOf(`employee_ref\n${'a'.repeat(51)}`), USD);
    expect(rows[0].issues).toEqual(['name_too_long']);
  });
  it('rejects a quota above the backend cap', () => {
    const rows = validateRows(
      rowsOf('employee_ref,quota\nE1,1000000000\nE2,1000000001'),
      USD,
    );
    expect(rows[0].issues).toEqual([]);
    expect(rows[1].issues).toEqual(['quota_too_large']);
    expect(rows[1].quota).toBeNull();
  });
});
