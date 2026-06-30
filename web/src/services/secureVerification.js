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
 * 2FA and Passkey verification have been delegated to the OIDC IdP.
 * Only session-based verification via /api/verify remains.
 */
export class SecureVerificationService {
  static async checkAvailableVerificationMethods() {
    return {
      has2FA: false,
      hasPasskey: false,
      passkeySupported: false,
    };
  }

  static async verify(method, code = '') {
    const verifyResponse = await API.post('/api/verify', {
      method,
      code: code?.trim() || '',
    });
    if (!verifyResponse.data?.success) {
      throw new Error(verifyResponse.data?.message || 'Verification failed');
    }
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
