import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';

import { ErrorState } from '@/components/error-state';
import { PageHeader } from '@/components/layout/page-header';
import { Skeleton } from '@/components/ui/skeleton';
import { parseMoneyConfig } from '@/lib/money';
import { ROLE, hasMinRole } from '@/lib/auth';
import { statusQueryOptions } from '@/lib/status';
import { currentUserQueryOptions } from '@/lib/user';

import { Marketplace } from './components/marketplace';
import { modelCatalogQueryOptions } from './lib/api';
import { usdPerRatio } from './lib/catalog';

function errorMessage(error: unknown): string | undefined {
  return error instanceof Error && error.message ? error.message : undefined;
}

export function ModelsPage() {
  const { t } = useTranslation();
  // The caller's group multiplier applies to every displayed price.
  const user = useQuery(currentUserQueryOptions);
  const group = user.data?.group || 'default';
  // The unit price (quota_per_unit) comes from /api/status via lib/money.ts;
  // without it no price can be computed, which is an error, not "unpriced".
  const status = useQuery(statusQueryOptions);
  const money = status.data ? parseMoneyConfig(status.data) : null;
  const catalog = useQuery({
    ...modelCatalogQueryOptions(
      group,
      money ? usdPerRatio(money) : 0,
      // Rankings are tenant-admin only; below that the read would just 403.
      hasMinRole(user.data?.role, ROLE.admin),
    ),
    enabled: user.isSuccess && money !== null,
  });

  let moneyError: Error | null = null;
  if (status.isError) moneyError = status.error;
  else if (status.isSuccess && money === null) {
    moneyError = new Error(t('Price configuration is unavailable'));
  }
  const failure = user.isError
    ? user.error
    : (moneyError ?? (catalog.isError ? catalog.error : null));
  const entries = catalog.data;
  const callable = entries?.filter((e) => e.routable).length ?? 0;

  let body: ReactNode;
  if (failure) {
    body = (
      <div data-testid='models-error'>
        <ErrorState
          title={t('Failed to load models')}
          description={errorMessage(failure)}
          onRetry={() => {
            if (user.isError) void user.refetch();
            else if (moneyError) void status.refetch();
            else void catalog.refetch();
          }}
        />
      </div>
    );
  } else if (entries) {
    body = <Marketplace entries={entries} />;
  } else {
    body = (
      <div className='space-y-3' data-testid='models-loading'>
        <Skeleton className='h-24 w-full' />
        <Skeleton className='h-24 w-full' />
        <Skeleton className='h-24 w-full' />
      </div>
    );
  }

  return (
    <>
      <PageHeader
        title={t('Models')}
        description={
          entries
            ? t(
                '{{n}} callable with your keys · prices per 1M tokens, after your group multiplier',
                { n: callable },
              )
            : undefined
        }
      />
      <div className='mt-6'>{body}</div>
    </>
  );
}
