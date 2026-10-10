import { useTranslation } from 'react-i18next'

import { PageHeader } from '@/components/layout/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { AllowlistSection } from './components/allowlist-section'
import { AuditSection } from './components/audit-section'
import { RetentionSection } from './components/retention-section'
import { RoutingSection } from './components/routing-section'
import { RulesSection } from './components/rules-section'
import { SedimentationSection } from './components/sedimentation-section'

/**
 * Data and security (tenant admins only; the route guard enforces it and the
 * handlers re-check). Six independent sections, each loading its own data so
 * one failing read does not blank the others.
 */
export function OrgPolicyPage() {
  const { t } = useTranslation()
  return (
    <div className='space-y-6'>
      <PageHeader
        title={t('Data & Security')}
        description={t('Control which models your organization can call, what logs keep, and what content may reach model providers.')}
      />
      <Tabs defaultValue='models'>
        <TabsList>
          <TabsTrigger value='models'>{t('Model allow-list')}</TabsTrigger>
          <TabsTrigger value='retention'>{t('Content retention')}</TabsTrigger>
          <TabsTrigger value='rules'>{t('Content rules')}</TabsTrigger>
          <TabsTrigger value='routing'>{t('Decision routing')}</TabsTrigger>
          <TabsTrigger value='archive'>{t('Data archiving')}</TabsTrigger>
          <TabsTrigger value='audit'>{t('Audit log')}</TabsTrigger>
        </TabsList>
        <TabsContent value='models'>
          <AllowlistSection />
        </TabsContent>
        <TabsContent value='retention'>
          <RetentionSection />
        </TabsContent>
        <TabsContent value='rules'>
          <RulesSection />
        </TabsContent>
        <TabsContent value='routing'>
          <RoutingSection />
        </TabsContent>
        <TabsContent value='archive'>
          <SedimentationSection />
        </TabsContent>
        <TabsContent value='audit'>
          <AuditSection />
        </TabsContent>
      </Tabs>
    </div>
  )
}
