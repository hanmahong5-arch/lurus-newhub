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
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../services/secureVerification', () => ({
  SecureVerificationService: {
    checkAvailableVerificationMethods: vi.fn(),
    verify: vi.fn(),
  },
}));

vi.mock('../../helpers', () => ({
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../helpers/secureApiCall', () => ({
  isVerificationRequiredError: vi.fn(() => false),
}));

vi.mock('react-i18next', () => {
  const translation = { t: (key) => key };
  return { useTranslation: () => translation };
});

import { useSecureVerification } from './useSecureVerification';
import { SecureVerificationService } from '../../services/secureVerification';
import { showError, showSuccess } from '../../helpers';
import { isVerificationRequiredError } from '../../helpers/secureApiCall';

const methods = (over = {}) => ({
  has2FA: false,
  hasPasskey: false,
  passkeySupported: false,
  hasSession: false,
  ...over,
});

const mount = async (options = {}) => {
  const hook = renderHook(() => useSecureVerification(options));
  await waitFor(() =>
    expect(
      SecureVerificationService.checkAvailableVerificationMethods,
    ).toHaveBeenCalled(),
  );
  return hook;
};

beforeEach(() => {
  vi.clearAllMocks();
  isVerificationRequiredError.mockReturnValue(false);
  SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
    methods({ has2FA: true }),
  );
  SecureVerificationService.verify.mockResolvedValue(undefined);
});

describe('useSecureVerification — capability discovery', () => {
  it('probes the available factors on mount', async () => {
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.verificationMethods.has2FA).toBe(true),
    );
    expect(result.current.hasAnyVerificationMethod).toBe(true);
    expect(result.current.isModalVisible).toBe(false);
  });

  it('reports no factor at all when the account has none', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods(),
    );
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.hasAnyVerificationMethod).toBe(false),
    );
    expect(result.current.getRecommendedMethod()).toBeNull();
  });

  it('canUseMethod requires passkey support, not just registration', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ hasPasskey: true, passkeySupported: false, has2FA: true }),
    );
    const { result } = await mount();

    await waitFor(() => expect(result.current.canUseMethod('2fa')).toBe(true));
    expect(result.current.canUseMethod('passkey')).toBe(false);
    expect(result.current.canUseMethod('session')).toBe(false);
    expect(result.current.canUseMethod('carrier-pigeon')).toBe(false);
  });

  it('recommends passkey over 2fa, and 2fa over session', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({
        hasPasskey: true,
        passkeySupported: true,
        has2FA: true,
        hasSession: true,
      }),
    );
    const all = await mount();
    await waitFor(() =>
      expect(all.result.current.getRecommendedMethod()).toBe('passkey'),
    );

    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ has2FA: true, hasSession: true }),
    );
    const noPasskey = await mount();
    await waitFor(() =>
      expect(noPasskey.result.current.getRecommendedMethod()).toBe('2fa'),
    );

    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ hasSession: true }),
    );
    const sessionOnly = await mount();
    await waitFor(() =>
      expect(sessionOnly.result.current.getRecommendedMethod()).toBe('session'),
    );
  });
});

describe('useSecureVerification — starting a challenge', () => {
  it('refuses to arm a challenge when no factor is enrolled', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods(),
    );
    const onError = vi.fn();
    const { result } = await mount({ onError });

    const apiCall = vi.fn();
    let armed;
    await act(async () => {
      armed = await result.current.startVerification(apiCall);
    });

    expect(armed).toBe(false);
    expect(result.current.isModalVisible).toBe(false);
    // The privileged call must NOT have run.
    expect(apiCall).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledTimes(1);
    expect(onError).toHaveBeenCalledTimes(1);
  });

  it('opens the modal on the strongest available factor', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ hasPasskey: true, passkeySupported: true, has2FA: true }),
    );
    const { result } = await mount();

    const apiCall = vi.fn();
    let armed;
    await act(async () => {
      armed = await result.current.startVerification(apiCall);
    });

    expect(armed).toBe(true);
    expect(result.current.isModalVisible).toBe(true);
    expect(result.current.currentMethod).toBe('passkey');
    expect(apiCall).not.toHaveBeenCalled();
  });

  it('honours an explicitly preferred factor', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ hasPasskey: true, passkeySupported: true, has2FA: true }),
    );
    const { result } = await mount();

    await act(async () => {
      await result.current.startVerification(vi.fn(), {
        preferredMethod: '2fa',
        title: 'Confirm deletion',
        description: 'This cannot be undone',
      });
    });

    expect(result.current.currentMethod).toBe('2fa');
    expect(result.current.verificationState.title).toBe('Confirm deletion');
    expect(result.current.verificationState.description).toBe(
      'This cannot be undone',
    );
  });

  it('falls back to the session factor when it is the only one', async () => {
    SecureVerificationService.checkAvailableVerificationMethods.mockResolvedValue(
      methods({ hasSession: true }),
    );
    const { result } = await mount();

    await act(async () => {
      await result.current.startVerification(vi.fn());
    });

    expect(result.current.currentMethod).toBe('session');
  });
});

describe('useSecureVerification — executing a challenge', () => {
  const armed = async (options = {}) => {
    const hook = await mount(options);
    await act(async () => {
      await hook.result.current.startVerification(options.apiCall ?? vi.fn(), {
        preferredMethod: '2fa',
      });
    });
    return hook;
  };

  it('refuses to run when no privileged call was armed', async () => {
    const { result } = await mount();

    await act(async () => {
      await result.current.executeVerification('2fa', '123456');
    });

    expect(SecureVerificationService.verify).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('验证配置错误');
  });

  it('verifies FIRST and only then runs the privileged call', async () => {
    const order = [];
    SecureVerificationService.verify.mockImplementation(async () => {
      order.push('verify');
    });
    const apiCall = vi.fn(async () => {
      order.push('apiCall');
      return { deleted: true };
    });
    const onSuccess = vi.fn();
    const { result } = await armed({
      apiCall,
      onSuccess,
      successMessage: 'token revoked',
    });

    let returned;
    await act(async () => {
      returned = await result.current.executeVerification('2fa', '123456');
    });

    expect(order).toEqual(['verify', 'apiCall']);
    expect(SecureVerificationService.verify).toHaveBeenCalledWith(
      '2fa',
      '123456',
    );
    expect(returned).toEqual({ deleted: true });
    expect(onSuccess).toHaveBeenCalledWith({ deleted: true }, '2fa');
    expect(showSuccess).toHaveBeenCalledWith('token revoked');
  });

  it('never runs the privileged call when verification fails', async () => {
    const apiCall = vi.fn();
    const onError = vi.fn();
    SecureVerificationService.verify.mockRejectedValue(new Error('wrong code'));
    const { result } = await armed({ apiCall, onError });

    await act(async () => {
      await expect(
        result.current.executeVerification('2fa', '000000'),
      ).rejects.toThrow('wrong code');
    });

    expect(apiCall).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('wrong code');
    expect(onError).toHaveBeenCalled();
    // The challenge stays armed so the user can retry.
    expect(result.current.isModalVisible).toBe(true);
    expect(result.current.isLoading).toBe(false);
  });

  it('falls back to a generic message when the failure carries none', async () => {
    SecureVerificationService.verify.mockRejectedValue(new Error(''));
    const { result } = await armed({ apiCall: vi.fn() });

    await act(async () => {
      await expect(
        result.current.executeVerification('2fa', '000000'),
      ).rejects.toThrow();
    });

    expect(showError).toHaveBeenCalledWith('验证失败，请重试');
  });

  it('surfaces a failure raised by the privileged call itself', async () => {
    const apiCall = vi.fn().mockRejectedValue(new Error('token already gone'));
    const onSuccess = vi.fn();
    const { result } = await armed({ apiCall, onSuccess });

    await act(async () => {
      await expect(
        result.current.executeVerification('2fa', '123456'),
      ).rejects.toThrow('token already gone');
    });

    expect(SecureVerificationService.verify).toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('token already gone');
  });

  it('closes the modal on success by default', async () => {
    const { result } = await armed({ apiCall: vi.fn(async () => 'ok') });

    await act(async () => {
      await result.current.executeVerification('2fa', '123456');
    });

    expect(result.current.isModalVisible).toBe(false);
    expect(result.current.currentMethod).toBeNull();
  });

  it('keeps the modal open when autoReset is off', async () => {
    const { result } = await armed({
      apiCall: vi.fn(async () => 'ok'),
      autoReset: false,
    });

    await act(async () => {
      await result.current.executeVerification('2fa', '123456');
    });

    expect(result.current.isModalVisible).toBe(true);
    expect(result.current.currentMethod).toBe('2fa');
  });

  it('stays quiet when no success message was configured', async () => {
    const { result } = await armed({ apiCall: vi.fn(async () => 'ok') });

    await act(async () => {
      await result.current.executeVerification('2fa', '123456');
    });

    expect(showSuccess).not.toHaveBeenCalled();
  });
});

describe('useSecureVerification — modal state', () => {
  it('records and clears the typed code', async () => {
    const { result } = await mount();

    act(() => result.current.setVerificationCode('654321'));
    expect(result.current.code).toBe('654321');

    // Switching factor wipes the half-typed code so it cannot leak across.
    act(() => result.current.switchVerificationMethod('passkey'));
    expect(result.current.currentMethod).toBe('passkey');
    expect(result.current.code).toBe('');
  });

  it('cancelling drops the armed call and shuts the modal', async () => {
    const apiCall = vi.fn();
    const { result } = await mount();

    await act(async () => {
      await result.current.startVerification(apiCall);
    });
    expect(result.current.isModalVisible).toBe(true);

    act(() => result.current.cancelVerification());

    expect(result.current.isModalVisible).toBe(false);
    expect(result.current.verificationState.apiCall).toBeNull();
    expect(apiCall).not.toHaveBeenCalled();
  });
});

describe('useSecureVerification — withVerification wrapper', () => {
  it('passes an unchallenged call straight through', async () => {
    const apiCall = vi.fn(async () => 'plain result');
    const { result } = await mount();

    let returned;
    await act(async () => {
      returned = await result.current.withVerification(apiCall);
    });

    expect(returned).toBe('plain result');
    expect(apiCall).toHaveBeenCalledTimes(1);
    expect(result.current.isModalVisible).toBe(false);
  });

  it('arms a challenge when the server demands step-up', async () => {
    isVerificationRequiredError.mockReturnValue(true);
    const apiCall = vi
      .fn()
      .mockRejectedValue(new Error('verification required'));
    const { result } = await mount();

    let returned;
    await act(async () => {
      returned = await result.current.withVerification(apiCall, {
        preferredMethod: '2fa',
      });
    });

    expect(returned).toBeNull();
    expect(result.current.isModalVisible).toBe(true);
    expect(result.current.currentMethod).toBe('2fa');
  });

  it('re-throws an unrelated failure instead of asking for a code', async () => {
    isVerificationRequiredError.mockReturnValue(false);
    const apiCall = vi.fn().mockRejectedValue(new Error('500 upstream'));
    const { result } = await mount();

    await act(async () => {
      await expect(result.current.withVerification(apiCall)).rejects.toThrow(
        '500 upstream',
      );
    });

    expect(result.current.isModalVisible).toBe(false);
  });
});
