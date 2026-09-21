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

import { classifyLoad, isLoadFailed } from './loadState';

// A resolved axios response: `.data` sits at the top level.
const axiosOk = (data) => ({ status: 200, data: { success: true, data } });
const axiosRefused = (message) => ({
  status: 200,
  data: { success: false, message },
});
// A rejected axios error: the body (if any) is nested under `.response`.
const axiosError = (status) => ({ response: { status } });
const networkReject = () => new Error('Network Error');

describe('classifyLoad — raw axios outcomes', () => {
  it('classifies a resolved 200 {success:true} response as ok', () => {
    expect(classifyLoad(axiosOk({ id: 1 }))).toBe('ok');
  });

  it('classifies a resolved 200 {success:false} carrying the v1 refusal message as forbidden', () => {
    // This codebase's session-auth branch answers "insufficient
    // permission" as HTTP 200 {success:false} rather than a 4xx
    // (middleware/auth.go:321 minRole check) — the same refusal a customer
    // sees from a 403 must not be misread as "an empty account".
    expect(classifyLoad(axiosRefused('无权进行此操作，权限不足'))).toBe(
      'forbidden',
    );
  });

  it('classifies a resolved 200 {success:false, error_code:PERMISSION_DENIED} as forbidden', () => {
    // The v2 envelope's machine-readable half (admin_jwt_auth.go's denial
    // table) — recognised without depending on the message wording.
    expect(
      classifyLoad({
        status: 200,
        data: { success: false, error_code: 'PERMISSION_DENIED', message: '' },
      }),
    ).toBe('forbidden');
  });

  it('classifies a resolved 200 {success:false} that is NOT a refusal as error, not forbidden', () => {
    // A handler answering 200 {success:false} for a non-permission reason
    // (a rejected filter window, a maintenance page) previously printed
    // "Admin access required" on the Users/Audit pages — a false claim on
    // the very pages this lane exists to make honest.
    expect(classifyLoad(axiosRefused('时间跨度不能超过 1 个月'))).toBe('error');
    expect(classifyLoad(axiosRefused('用户已被封禁'))).toBe('error');
    expect(classifyLoad(axiosRefused(undefined))).toBe('error');
    expect(classifyLoad({ status: 200, data: 'a login page, not JSON' })).toBe(
      'error',
    );
  });

  it('classifies a rejected 401 as unauthenticated, not forbidden', () => {
    expect(classifyLoad(axiosError(401))).toBe('unauthenticated');
  });

  it('classifies a rejected 403 as forbidden', () => {
    expect(classifyLoad(axiosError(403))).toBe('forbidden');
  });

  it('classifies a rejected 5xx as error', () => {
    expect(classifyLoad(axiosError(502))).toBe('error');
    expect(classifyLoad(axiosError(500))).toBe('error');
  });

  it('classifies a network-level rejection (no response at all) as error', () => {
    expect(classifyLoad(networkReject())).toBe('error');
  });

  it('classifies undefined/null as error rather than throwing', () => {
    expect(classifyLoad(undefined)).toBe('error');
    expect(classifyLoad(null)).toBe('error');
  });
});

describe('classifyLoad — Promise.allSettled() entries', () => {
  it('unwraps a fulfilled entry and classifies its value', async () => {
    const [settled] = await Promise.allSettled([
      Promise.resolve(axiosOk({ id: 7 })),
    ]);
    expect(settled.status).toBe('fulfilled');
    expect(classifyLoad(settled)).toBe('ok');
  });

  it('unwraps a fulfilled entry carrying the v1 refusal body as forbidden', async () => {
    const [settled] = await Promise.allSettled([
      Promise.resolve(axiosRefused('无权进行此操作，权限不足')),
    ]);
    expect(classifyLoad(settled)).toBe('forbidden');
  });

  it('unwraps a fulfilled entry carrying an unrecognised {success:false} body as error', async () => {
    const [settled] = await Promise.allSettled([
      Promise.resolve(axiosRefused('nope')),
    ]);
    expect(classifyLoad(settled)).toBe('error');
  });

  it('unwraps a rejected entry and classifies its reason', async () => {
    const [settled] = await Promise.allSettled([
      Promise.reject(axiosError(403)),
    ]);
    expect(settled.status).toBe('rejected');
    expect(classifyLoad(settled)).toBe('forbidden');
  });

  it('unwraps a rejected entry with a 401 reason as unauthenticated', async () => {
    const [settled] = await Promise.allSettled([
      Promise.reject(axiosError(401)),
    ]);
    expect(classifyLoad(settled)).toBe('unauthenticated');
  });

  it('unwraps a rejected entry with a network-level reason as error', async () => {
    const [settled] = await Promise.allSettled([
      Promise.reject(networkReject()),
    ]);
    expect(classifyLoad(settled)).toBe('error');
  });
});

describe('isLoadFailed', () => {
  it('is false for ok', () => {
    expect(isLoadFailed('ok')).toBe(false);
  });

  it('is false for the not-yet-attempted sentinel (null/undefined)', () => {
    expect(isLoadFailed(null)).toBe(false);
    expect(isLoadFailed(undefined)).toBe(false);
  });

  it('is true for every non-ok classifyLoad outcome', () => {
    expect(isLoadFailed('forbidden')).toBe(true);
    expect(isLoadFailed('unauthenticated')).toBe(true);
    expect(isLoadFailed('error')).toBe(true);
  });
});
