import { useTranslation } from 'react-i18next'

/** Translated names for the rule enums; unknown values pass through. */
export function useRuleLabels() {
  const { t } = useTranslation()
  return {
    role: (v: string) =>
      ({
        any: t('Any role'),
        system: t('System messages'),
        user: t('User messages'),
        assistant: t('Assistant messages'),
      })[v] ?? v,
    kind: (v: string) =>
      ({ mask: t('Mask matches'), reject: t('Reject request') })[v] ?? v,
    patternType: (v: string) =>
      ({ builtin: t('Built-in'), regex: t('Regular expression') })[v] ?? v,
    mode: (v: string) =>
      ({ observe: t('Observe'), enforce: t('Enforce') })[v] ?? v,
  }
}
