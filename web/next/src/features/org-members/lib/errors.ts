import { isApiError } from '@/lib/api'

type T = (key: string) => string

function codeOf(error: unknown): string | undefined {
  return isApiError(error) ? error.code : undefined
}

function fallback(error: unknown, t: T): string {
  return isApiError(error) ? error.message : t('Request failed')
}

/** Redeem failures, keyed by the server's error_code. */
export function redeemErrorMessage(error: unknown, t: T): string {
  switch (codeOf(error)) {
    case 'INVITE_EXPIRED':
      return t('This invitation has expired. Ask an admin for a new one.')
    case 'INVITE_REVOKED':
      return t('This invitation was revoked by an admin.')
    case 'INVITE_ALREADY_CONSUMED':
      return t('This invitation has already been used.')
    case 'INVITE_NOT_FOUND':
      return t('Invitation not found. Check the code and try again.')
    case 'INVITE_CODE_REQUIRED':
      return t('Enter the invitation code.')
    case 'TENANT_ROLE_FORBIDDEN_IN_DEFAULT':
      return t('Roles are not available in the shared default tenant.')
    default:
      return fallback(error, t)
  }
}

/** Role-change failures. */
export function roleErrorMessage(error: unknown, t: T): string {
  switch (codeOf(error)) {
    case 'LAST_TENANT_ADMIN':
      return t(
        'This is the last administrator of the organization. Promote another member to administrator first.'
      )
    case 'TENANT_ROLE_INVALID':
      return t('That role is not valid.')
    case 'TENANT_ROLE_FORBIDDEN_IN_DEFAULT':
      return t('Roles are not available in the shared default tenant.')
    default:
      return fallback(error, t)
  }
}

export function inviteErrorMessage(error: unknown, t: T): string {
  switch (codeOf(error)) {
    case 'INVITE_TTL_INVALID':
      return t('The validity must be between 1 hour and 30 days.')
    case 'INVITE_NOT_FOUND':
      return t('Invitation not found or no longer pending.')
    case 'TENANT_ROLE_FORBIDDEN_IN_DEFAULT':
      return t('Roles are not available in the shared default tenant.')
    default:
      return fallback(error, t)
  }
}
