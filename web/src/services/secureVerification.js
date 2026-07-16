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

import { API } from '../helpers';

/**
 * Secure verification service.
 * The backend (UniversalVerify) accepts method "session" only for users
 * WITHOUT an active TOTP enrollment; enrolled users must present a valid
 * TOTP code (method "totp"). GET /api/verify/status reports totp_enrolled
 * so the modal can offer the right factor. Session stays reported as
 * available for unenrolled users or the step-up flow dead-ends before ever
 * calling /api/verify.
 */
export class SecureVerificationService {
  static async checkAvailableVerificationMethods() {
    let totpEnrolled = false;
    try {
      const res = await API.get('/api/verify/status');
      totpEnrolled = !!res.data?.data?.totp_enrolled;
    } catch (e) {
      // Status probe failure: fall back to session so the flow can proceed;
      // the backend still enforces TOTP for enrolled users.
    }
    return {
      has2FA: totpEnrolled,
      hasPasskey: false,
      passkeySupported: false,
      hasSession: !totpEnrolled,
    };
  }

  static async verify(method, code = '') {
    // UI method key '2fa' maps to the backend factor name 'totp'.
    const apiMethod = method === '2fa' ? 'totp' : method;
    const verifyResponse = await API.post('/api/verify', {
      method: apiMethod,
      code: code?.trim() || '',
    });
    if (!verifyResponse.data?.success) {
      throw new Error(verifyResponse.data?.message || 'Verification failed');
    }
  }
}

/**
 * TOTP enrollment management (personal settings → security).
 */
export class TotpService {
  static async getStatus() {
    const res = await API.get('/api/user/totp/status');
    if (!res.data?.success) {
      throw new Error(res.data?.message || 'Failed to load TOTP status');
    }
    return res.data.data; // { enrolled, pending }
  }

  static async enroll() {
    const res = await API.post('/api/user/totp/enroll', {});
    if (!res.data?.success) {
      throw new Error(res.data?.message || 'Failed to start TOTP enrollment');
    }
    return res.data.data; // { secret, otpauth_url }
  }

  static async confirm(code) {
    const res = await API.post('/api/user/totp/confirm', {
      code: code?.trim() || '',
    });
    if (!res.data?.success) {
      throw new Error(res.data?.message || 'Failed to confirm TOTP code');
    }
  }

  static async disable() {
    const res = await API.post('/api/user/totp/disable', {});
    return res.data;
  }
}

/**
 * Pre-built API call factories for secure actions
 */
export const createApiCalls = {
  viewChannelKey: (channelId) => async () => {
    const response = await API.post(`/api/channel/${channelId}/key`, {});
    return response.data;
  },

  custom:
    (url, method = 'POST', extraData = {}) =>
    async () => {
      const data = extraData;
      let response;
      switch (method.toUpperCase()) {
        case 'GET':
          response = await API.get(url, { params: data });
          break;
        case 'POST':
          response = await API.post(url, data);
          break;
        case 'PUT':
          response = await API.put(url, data);
          break;
        case 'DELETE':
          response = await API.delete(url, { data });
          break;
        default:
          throw new Error(`Unsupported HTTP method: ${method}`);
      }
      return response.data;
    },
};
