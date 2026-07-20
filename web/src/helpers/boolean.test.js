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
import { describe, it, expect } from 'vitest';

import { toBoolean } from './boolean';

describe('toBoolean', () => {
  it('passes native booleans through unchanged', () => {
    expect(toBoolean(true)).toBe(true);
    expect(toBoolean(false)).toBe(false);
  });

  it('treats the number 1 as true, all other numbers as false', () => {
    expect(toBoolean(1)).toBe(true);
    expect(toBoolean(0)).toBe(false);
    expect(toBoolean(-1)).toBe(false);
    expect(toBoolean(2)).toBe(false);
  });

  it('treats "true"/"1" strings (case-insensitive) as true', () => {
    expect(toBoolean('true')).toBe(true);
    expect(toBoolean('TRUE')).toBe(true);
    expect(toBoolean('True')).toBe(true);
    expect(toBoolean('1')).toBe(true);
  });

  it('treats other strings, including "false"/"0", as false', () => {
    expect(toBoolean('false')).toBe(false);
    expect(toBoolean('FALSE')).toBe(false);
    expect(toBoolean('0')).toBe(false);
    expect(toBoolean('yes')).toBe(false);
    expect(toBoolean('')).toBe(false);
  });

  it('falls back to false for null/undefined/objects/arrays', () => {
    expect(toBoolean(null)).toBe(false);
    expect(toBoolean(undefined)).toBe(false);
    expect(toBoolean({})).toBe(false);
    expect(toBoolean([])).toBe(false);
  });
});
