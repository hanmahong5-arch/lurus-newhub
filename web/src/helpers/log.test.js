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

import { getLogOther } from './log';

describe('getLogOther', () => {
  it('parses a JSON object string into an object', () => {
    expect(getLogOther('{"model":"gpt-4","tokens":10}')).toEqual({
      model: 'gpt-4',
      tokens: 10,
    });
  });

  it('defaults to an empty object for undefined input', () => {
    expect(getLogOther(undefined)).toEqual({});
  });

  it('defaults to an empty object for an empty string', () => {
    expect(getLogOther('')).toEqual({});
  });

  it('parses the literal string "null" to null (not defaulted)', () => {
    // Only undefined/'' are special-cased to '{}'; any other input,
    // including the string "null", goes straight to JSON.parse.
    expect(getLogOther('null')).toBeNull();
  });

  it('parses arrays and primitives just like JSON.parse', () => {
    expect(getLogOther('[1,2,3]')).toEqual([1, 2, 3]);
    expect(getLogOther('42')).toBe(42);
    expect(getLogOther('"hello"')).toBe('hello');
  });

  it('throws on malformed JSON (does not swallow the error)', () => {
    expect(() => getLogOther('{not valid json')).toThrow();
  });
});
