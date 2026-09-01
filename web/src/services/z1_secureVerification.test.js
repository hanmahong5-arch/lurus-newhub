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
import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import { API } from '../helpers';
import {
  SecureVerificationService,
  TotpService,
  createApiCalls,
} from './secureVerification';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('SecureVerificationService.checkAvailableVerificationMethods', () => {
  it('offers TOTP (and withholds session) for an enrolled user', async () => {
    API.get.mockResolvedValue({ data: { data: { totp_enrolled: true } } });

    const methods =
      await SecureVerificationService.checkAvailableVerificationMethods();

    expect(API.get).toHaveBeenCalledWith('/api/verify/status');
    expect(methods).toEqual({
      has2FA: true,
      hasSession: false,
    });
  });

  it('offers session (and withholds TOTP) for an unenrolled user', async () => {
    API.get.mockResolvedValue({ data: { data: { totp_enrolled: false } } });

    const methods =
      await SecureVerificationService.checkAvailableVerificationMethods();

    expect(methods.has2FA).toBe(false);
    expect(methods.hasSession).toBe(true);
  });

  // has2FA and hasSession are strict complements — the step-up modal picks
  // exactly one factor from them, so both-true or both-false would either
  // let an enrolled user skip TOTP or dead-end the flow entirely.
  it.each([
    [{ data: { data: { totp_enrolled: true } } }],
    [{ data: { data: { totp_enrolled: false } } }],
    [{ data: { data: {} } }],
    [{ data: {} }],
    [{}],
  ])('has2FA and hasSession stay mutually exclusive (%#)', async (response) => {
    API.get.mockResolvedValue(response);
    const m =
      await SecureVerificationService.checkAvailableVerificationMethods();
    expect(m.has2FA).toBe(!m.hasSession);
  });

  it('treats a status response missing data.data as unenrolled', async () => {
    API.get.mockResolvedValue({ data: { success: true } });
    const m =
      await SecureVerificationService.checkAvailableVerificationMethods();
    expect(m.has2FA).toBe(false);
    expect(m.hasSession).toBe(true);
  });

  // Documented fallback: when the status probe fails the UI proceeds with
  // the session factor rather than locking the user out. The server-side
  // UniversalVerify still refuses "session" for enrolled accounts, so the
  // worst case is a rejected step-up, not a bypass.
  it('falls back to session when the status probe rejects', async () => {
    API.get.mockRejectedValue(new Error('network down'));

    const m =
      await SecureVerificationService.checkAvailableVerificationMethods();

    expect(m).toEqual({
      has2FA: false,
      hasSession: true,
    });
  });
});

describe('SecureVerificationService.verify', () => {
  it("maps the UI factor key '2fa' onto the backend factor 'totp'", async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await SecureVerificationService.verify('2fa', '123456');

    expect(API.post).toHaveBeenCalledWith('/api/verify', {
      method: 'totp',
      code: '123456',
    });
  });

  it('passes any other factor name through untouched', async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await SecureVerificationService.verify('session');

    expect(API.post).toHaveBeenCalledWith('/api/verify', {
      method: 'session',
      code: '',
    });
  });

  it('trims the submitted code before sending it', async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await SecureVerificationService.verify('2fa', '  123456\n');

    expect(API.post.mock.calls[0][1].code).toBe('123456');
  });

  it('normalises a whitespace-only code to the empty string', async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await SecureVerificationService.verify('2fa', '   ');

    expect(API.post.mock.calls[0][1].code).toBe('');
  });

  it('rejects with the backend message when verification fails', async () => {
    API.post.mockResolvedValue({
      data: { success: false, message: '验证码错误' },
    });

    await expect(
      SecureVerificationService.verify('2fa', '000000'),
    ).rejects.toThrow('验证码错误');
  });

  it('rejects with a generic message when the backend supplies none', async () => {
    API.post.mockResolvedValue({ data: { success: false } });

    await expect(
      SecureVerificationService.verify('2fa', '000000'),
    ).rejects.toThrow('Verification failed');
  });

  // Fail-closed check: an empty/garbled body must NOT be read as success.
  it.each([[{}], [{ data: null }], [{ data: undefined }]])(
    'rejects when the response body carries no success flag (%#)',
    async (response) => {
      API.post.mockResolvedValue(response);
      await expect(
        SecureVerificationService.verify('2fa', '123456'),
      ).rejects.toThrow('Verification failed');
    },
  );

  it('resolves with no value when the backend confirms success', async () => {
    API.post.mockResolvedValue({ data: { success: true, message: 'ok' } });
    await expect(
      SecureVerificationService.verify('2fa', '123456'),
    ).resolves.toBeUndefined();
  });

  // DEFECT (see report): the code is trimmed with `code?.trim()`, which
  // assumes a string. A numeric one-time code (e.g. from a controlled input
  // bound to a number field) blows up with "code.trim is not a function"
  // before the request is ever sent, and the surrounding hook surfaces that
  // as a generic failure. Unskip once the code is coerced (String(code)).
  it.skip('accepts a numeric code by coercing it to a string', async () => {
    API.post.mockResolvedValue({ data: { success: true } });
    await SecureVerificationService.verify('2fa', 123456);
    expect(API.post.mock.calls[0][1].code).toBe('123456');
  });

  it('currently throws a TypeError for a numeric code', async () => {
    API.post.mockResolvedValue({ data: { success: true } });
    await expect(
      SecureVerificationService.verify('2fa', 123456),
    ).rejects.toThrow(TypeError);
    expect(API.post).not.toHaveBeenCalled();
  });
});

describe('TotpService', () => {
  it('getStatus returns the enrolment payload on success', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { enrolled: true, pending: false } },
    });

    await expect(TotpService.getStatus()).resolves.toEqual({
      enrolled: true,
      pending: false,
    });
    expect(API.get).toHaveBeenCalledWith('/api/user/totp/status');
  });

  it('getStatus rejects with the backend message on failure', async () => {
    API.get.mockResolvedValue({ data: { success: false, message: '未登录' } });
    await expect(TotpService.getStatus()).rejects.toThrow('未登录');
  });

  it('getStatus rejects with a generic message when none is supplied', async () => {
    API.get.mockResolvedValue({ data: { success: false } });
    await expect(TotpService.getStatus()).rejects.toThrow(
      'Failed to load TOTP status',
    );
  });

  it('enroll posts an empty body and returns the secret material', async () => {
    API.post.mockResolvedValue({
      data: {
        success: true,
        data: { secret: 'JBSWY3DP', otpauth_url: 'otpauth://totp/x' },
      },
    });

    await expect(TotpService.enroll()).resolves.toEqual({
      secret: 'JBSWY3DP',
      otpauth_url: 'otpauth://totp/x',
    });
    expect(API.post).toHaveBeenCalledWith('/api/user/totp/enroll', {});
  });

  it('enroll rejects with a generic message when the backend refuses', async () => {
    API.post.mockResolvedValue({ data: { success: false } });
    await expect(TotpService.enroll()).rejects.toThrow(
      'Failed to start TOTP enrollment',
    );
  });

  it('confirm trims the code and resolves on success', async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await expect(TotpService.confirm(' 654321 ')).resolves.toBeUndefined();
    expect(API.post).toHaveBeenCalledWith('/api/user/totp/confirm', {
      code: '654321',
    });
  });

  it('confirm sends an empty code when called with nothing', async () => {
    API.post.mockResolvedValue({ data: { success: true } });
    await TotpService.confirm();
    expect(API.post.mock.calls[0][1].code).toBe('');
  });

  it('confirm rejects with the backend message on a wrong code', async () => {
    API.post.mockResolvedValue({
      data: { success: false, message: 'invalid code' },
    });
    await expect(TotpService.confirm('000000')).rejects.toThrow('invalid code');
  });

  // DEFECT (see report): disable() is the only TotpService method that does
  // NOT check `success`. A server-side refusal resolves normally, so a
  // caller written like the other three (`await TotpService.disable()` then
  // report success) would tell the user two-factor auth is off while it is
  // still armed. Unskip once disable() validates success like its siblings.
  it.skip('disable rejects when the backend refuses', async () => {
    API.post.mockResolvedValue({
      data: { success: false, message: 'verification required' },
    });
    await expect(TotpService.disable()).rejects.toThrow(
      'verification required',
    );
  });

  it('disable currently resolves even when the backend refuses', async () => {
    API.post.mockResolvedValue({
      data: { success: false, message: 'verification required' },
    });

    await expect(TotpService.disable()).resolves.toEqual({
      success: false,
      message: 'verification required',
    });
    expect(API.post).toHaveBeenCalledWith('/api/user/totp/disable', {});
  });
});

describe('createApiCalls.viewChannelKey', () => {
  it('builds a deferred POST to the channel key endpoint', async () => {
    API.post.mockResolvedValue({ data: { success: true, data: 'sk-abc' } });

    const call = createApiCalls.viewChannelKey(42);
    // Factory alone must not touch the network — the request only fires
    // after step-up verification succeeds.
    expect(API.post).not.toHaveBeenCalled();

    await expect(call()).resolves.toEqual({ success: true, data: 'sk-abc' });
    expect(API.post).toHaveBeenCalledWith('/api/channel/42/key', {});
  });

  it('propagates a rejected request to the caller', async () => {
    API.post.mockRejectedValue(new Error('403 forbidden'));
    await expect(createApiCalls.viewChannelKey(1)()).rejects.toThrow(
      '403 forbidden',
    );
  });
});

describe('createApiCalls.custom', () => {
  it('defaults to POST with an empty body', async () => {
    API.post.mockResolvedValue({ data: { success: true } });

    await createApiCalls.custom('/api/user/totp/disable')();

    expect(API.post).toHaveBeenCalledWith('/api/user/totp/disable', {});
  });

  it('sends GET data as query params, not as a body', async () => {
    API.get.mockResolvedValue({ data: { ok: 1 } });

    const result = await createApiCalls.custom('/api/thing', 'GET', {
      id: 3,
    })();

    expect(API.get).toHaveBeenCalledWith('/api/thing', { params: { id: 3 } });
    expect(result).toEqual({ ok: 1 });
  });

  it('sends PUT data as the request body', async () => {
    API.put.mockResolvedValue({ data: { ok: 1 } });
    await createApiCalls.custom('/api/thing', 'PUT', { a: 1 })();
    expect(API.put).toHaveBeenCalledWith('/api/thing', { a: 1 });
  });

  it('sends DELETE data under the axios `data` key', async () => {
    API.delete.mockResolvedValue({ data: { ok: 1 } });
    await createApiCalls.custom('/api/thing', 'DELETE', { a: 1 })();
    expect(API.delete).toHaveBeenCalledWith('/api/thing', { data: { a: 1 } });
  });

  it('accepts a lower-case method name', async () => {
    API.post.mockResolvedValue({ data: { ok: 1 } });
    await createApiCalls.custom('/api/thing', 'post', { a: 1 })();
    expect(API.post).toHaveBeenCalledWith('/api/thing', { a: 1 });
  });

  it('rejects an unsupported method instead of silently doing nothing', async () => {
    await expect(
      createApiCalls.custom('/api/thing', 'PATCH')(),
    ).rejects.toThrow('Unsupported HTTP method: PATCH');
    expect(API.post).not.toHaveBeenCalled();
    expect(API.get).not.toHaveBeenCalled();
  });
});
