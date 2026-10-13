import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';

import { ErrorState } from '@/components/error-state';
import { PageHeader } from '@/components/layout/page-header';
import { parseMoneyConfig } from '@/lib/money';
import { ROLE, hasMinRole } from '@/lib/auth';
import { statusQueryOptions } from '@/lib/status';
import { currentUserQueryOptions } from '@/lib/user';

import { MarketplaceSkeleton } from './components/loading-skeleton';
import { Marketplace } from './components/marketplace';
import {
  modelPerformanceQueryOptions,
  modelSourcesQueryOptions,
} from './lib/api';
import { buildCatalog, usdPerRatio } from './lib/catalog';
import type { PerfWindow } from './lib/perf';

function errorMessage(error: unknown): string | undefined {
  return error instanceof Error && error.message ? error.message : undefined;
}

/** "x0.5", "x1", "x1.25": a multiplier without trailing zeros. */
const formatRatio = (r: number) => `x${Number(r.toFixed(3))}`;

export function ModelsPage() {
  const { t } = useTranslation();
  // The caller's group multiplier applies to every displayed price.
  const user = useQuery(currentUserQueryOptions);
  const callerGroup = user.data?.group || 'default';
  const [priceGroup, setPriceGroup] = useState('');
  const [perfWindow, setPerfWindow] = useState<PerfWindow>(24);
  // The unit price (quota_per_unit) comes from /api/status via lib/money.ts;
  // without it no price can be computed, which is an error, not "unpriced".
  const status = useQuery(statusQueryOptions);
  const money = status.data ? parseMoneyConfig(status.data) : null;
  const sources = useQuery({
    ...modelSourcesQueryOptions(
      // Rankings are tenant-admin only; below that the read would just 403.
      hasMinRole(user.data?.role, ROLE.admin),
    ),
    enabled: user.isSuccess && money !== null,
  });
  // Decorative and never rejects; the previous window stays on screen while
  // another one loads.
  const perf = useQuery({
    ...modelPerformanceQueryOptions(perfWindow),
    enabled: sources.isSuccess,
    placeholderData: keepPreviousData,
  });

  const activeGroup = priceGroup || callerGroup;
  const groupRatios = sources.data?.groupRatio;
  const activeRatio = groupRatios?.[activeGroup] ?? 1;
  const unitPrice = money ? usdPerRatio(money) : 0;

  const entries = useMemo(
    () =>
      sources.data && perf.data
        ? buildCatalog({
            routable: sources.data.routable,
            pricing: sources.data.pricing,
            catalogue: sources.data.catalogue,
            usage: sources.data.usage,
            performance: perf.data,
            groupRatio: activeRatio,
            usdPerRatio: unitPrice,
          })
        : undefined,
    [sources.data, perf.data, activeRatio, unitPrice],
  );
  const groups = useMemo(
    () =>
      Object.entries(groupRatios ?? {})
        .filter(([name]) => name && name !== 'auto')
        .map(([name, ratio]) => ({ name, ratio }))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [groupRatios],
  );

  let moneyError: Error | null = null;
  if (status.isError) moneyError = status.error;
  else if (status.isSuccess && money === null) {
    moneyError = new Error(t('Price configuration is unavailable'));
  }
  const failure = user.isError
    ? user.error
    : (moneyError ?? (sources.isError ? sources.error : null));
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
            else void sources.refetch();
          }}
        />
      </div>
    );
  } else if (entries) {
    body = (
      <Marketplace
        entries={entries}
        groups={groups}
        groupRatios={groupRatios ?? {}}
        priceGroup={priceGroup}
        onPriceGroupChange={setPriceGroup}
        activeRatio={activeRatio}
        perfWindow={perfWindow}
        onPerfWindowChange={setPerfWindow}
      />
    );
  } else {
    body = <MarketplaceSkeleton />;
  }

  return (
    <>
      <PageHeader
        title={t('Models')}
        description={
          entries
            ? t(
                '{{n}} callable with your keys · prices use the {{group}} group multiplier ({{ratio}})',
                { n: callable, group: activeGroup, ratio: formatRatio(activeRatio) },
              )
            : undefined
        }
      />
      <div className='mt-6'>{body}</div>
    </>
  );
}
