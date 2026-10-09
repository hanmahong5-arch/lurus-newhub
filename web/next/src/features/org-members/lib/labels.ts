import { useTranslation } from 'react-i18next'

import type { MemberRole } from '../types'

export function useRoleLabel() {
  const { t } = useTranslation()
  return (role: MemberRole): string => {
    if (role === 'admin') return t('Administrator')
    if (role === 'dept_lead') return t('Department lead')
    return t('Member')
  }
}

export function roleVariant(role: MemberRole) {
  if (role === 'admin') return 'info' as const
  if (role === 'dept_lead') return 'purple' as const
  return 'neutral' as const
}
