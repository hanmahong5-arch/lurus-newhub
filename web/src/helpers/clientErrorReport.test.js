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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  reportClientError,
  MAX_REPORTS_PER_LOAD,
  __resetClientErrorReportsForTest,
} from './clientErrorReport';

describe('reportClientError', () => {
  let beacon;
  beforeEach(() => {
    __resetClientErrorReportsForTest();
    beacon = vi.fn(() => true);
    Object.defineProperty(navigator, 'sendBeacon', {
      value: beacon,
      configurable: true,
    });
    window.history.pushState({}, '', '/bridge-login?x=1#t=secret-token');
  });
  afterEach(() => {
    window.history.pushState({}, '', '/');
  });

  const lastBody = async () =>
    JSON.parse(await beacon.mock.calls.at(-1)[1].text());

  it('sends kind, page path and message to /api/client-error', async () => {
    reportClientError('render', new Error('x is undefined'));
    expect(beacon).toHaveBeenCalledTimes(1);
    expect(beacon.mock.calls[0][0]).toMatch(/\/api\/client-error$/);
    const body = await lastBody();
    expect(body.kind).toBe('render');
    expect(body.message).toBe('x is undefined');
    expect(body.page).toBe('/bridge-login');
  });

  it('never sends the query or hash, which can carry a login token', async () => {
    reportClientError('window_error', 'boom');
    const raw = await beacon.mock.calls[0][1].text();
    expect(raw).not.toContain('secret-token');
    expect(raw).not.toContain('x=1');
  });

  it('stops after the per-load cap so a render loop cannot flood the endpoint', () => {
    for (let i = 0; i < MAX_REPORTS_PER_LOAD + 10; i++) {
      reportClientError('render', new Error('loop'));
    }
    expect(beacon).toHaveBeenCalledTimes(MAX_REPORTS_PER_LOAD);
  });

  it('bounds the message', async () => {
    reportClientError('render', new Error('a'.repeat(5000)));
    expect((await lastBody()).message.length).toBe(300);
  });
});
