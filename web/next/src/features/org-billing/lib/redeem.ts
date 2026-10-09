import type { TFunction } from 'i18next'

import { ApiError } from '@/lib/api'

/** Redemption codes are exactly 32 characters (RedeemCodeV2). UX guard only. */
export const REDEEM_CODE_LENGTH = 32

/**
 * Same semantics as the legacy console's redeemFailure + errorMessages: a
 * coded failure becomes the localized sentence, an uncoded one falls back to
 * the server message, then to the generic line.
 */
export function redeemErrorMessage(err: unknown, t: TFunction): string {
  const code = err instanceof ApiError ? err.code : undefined
  switch (code) {
    case 'REDEMPTION_INVALID':
      return t('That redemption code is invalid or does not exist.')
    case 'REDEMPTION_USED':
      return t('That redemption code has already been used.')
    case 'REDEMPTION_EXPIRED':
      return t('That redemption code has expired.')
    case 'REDEMPTION_TENANT_MISMATCH':
      return t('That redemption code does not belong to this tenant.')
    case 'REDEMPTION_FAILED':
      return t('Redemption failed, please try again later.')
    default:
      break
  }
  if (err instanceof ApiError && err.message.trim()) return err.message
  return t('Redemption failed')
}
