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
import { beforeEach, describe, expect, it, vi, type MockInstance } from 'vitest'

import { http } from '@/lib/api'

import {
  confirmTotp,
  disableTotp,
  fetchSessions,
  regenerateBackupCodes,
  revokeOtherSessions,
  revokeSession,
  saveUserSettings,
} from './api'

// restoreMocks is on, so the spies are (re)created for every test.
let request: MockInstance<typeof http.request>
let del: MockInstance<typeof http.delete>

function reply(data: unknown, status = 200) {
  request.mockResolvedValueOnce({ data, status } as never)
}

const SETTINGS = {
  notify_type: 'email' as const,
  quota_warning_threshold: 10,
  accept_unset_model_ratio_model: false,
  record_ip_log: false,
}

beforeEach(() => {
  request = vi.spyOn(http, 'request')
  del = vi.spyOn(http, 'delete')
  request.mockReset()
  del.mockReset()
})

describe('account api wire contract', () => {
  it('revokes one session with a bodiless 204', async () => {
    del.mockResolvedValueOnce({ data: '', status: 204 } as never)
    await expect(revokeSession(5)).resolves.toBeUndefined()
    expect(del.mock.calls[0][0]).toBe('/api/v2/~/sessions/5')
  })

  it('lets a failed revoke reject', async () => {
    del.mockRejectedValueOnce(new Error('Session not found'))
    await expect(revokeSession(5)).rejects.toThrow('Session not found')
  })

  it('signs out the other sessions and reports how many', async () => {
    reply({ success: true, data: { revoked: 3 } })
    expect(await revokeOtherSessions()).toEqual({ revoked: 3 })
    expect(request.mock.calls[0][0]).toMatchObject({
      method: 'DELETE',
      url: '/api/v2/~/sessions/others',
    })
  })

  it('reads the session list through the self tenant', async () => {
    reply({
      success: true,
      data: { registry_enabled: true, items: [{ id: 2, is_current: true }] },
    })
    const list = await fetchSessions()
    expect(request.mock.calls[0][0].url).toBe('/api/v2/~/sessions')
    expect(list.items[0]).toMatchObject({ id: 2, current: true })
  })

  it('posts the settings to the v2 tenant route and unwraps a 200 failure', async () => {
    reply({ success: true, message: '' })
    await saveUserSettings(SETTINGS)
    expect(request.mock.calls[0][0]).toMatchObject({
      method: 'PUT',
      url: '/api/v2/~/user/setting',
    })
    reply({ success: false, message: 'invalid warning type' })
    await expect(saveUserSettings(SETTINGS)).rejects.toMatchObject({
      message: 'invalid warning type',
    })
  })

  it('uses the totp routes with the documented bodies', async () => {
    reply({ success: true, data: { backup_codes: ['a'] } })
    await confirmTotp('123456')
    expect(request.mock.calls[0][0]).toMatchObject({
      url: '/api/v2/~/user/totp/confirm',
      data: { code: '123456' },
    })
    reply({ success: true, data: { backup_codes: ['b'] } })
    await regenerateBackupCodes()
    expect(request.mock.calls[1][0].url).toBe(
      '/api/v2/~/user/totp/backup-codes/regenerate'
    )
    reply({ success: true, message: '' })
    await disableTotp()
    expect(request.mock.calls[2][0]).toMatchObject({
      method: 'POST',
      url: '/api/v2/~/user/totp/disable',
    })
  })
})
