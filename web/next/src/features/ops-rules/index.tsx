import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { RulesPanel } from './components/rules-panel'
import { TemplatesPanel } from './components/templates-panel'

export function OpsRulesPage() {
  const { t } = useTranslation()
  return (
    <>
      <PageHeader
        title={t('Platform Rules & Templates')}
        description={t(
          'Content rules that apply to every tenant, and override templates for channels.'
        )}
      />
      <Tabs defaultValue='rules'>
        <TabsList>
          <TabsTrigger value='rules'>{t('Content rules')}</TabsTrigger>
          <TabsTrigger value='templates'>{t('Override templates')}</TabsTrigger>
        </TabsList>
        <TabsContent value='rules'>
          <RulesPanel />
        </TabsContent>
        <TabsContent value='templates'>
          <TemplatesPanel />
        </TabsContent>
      </Tabs>
    </>
  )
}
