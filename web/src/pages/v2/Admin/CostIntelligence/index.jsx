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
import React, { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../../components/hifi/HFShell';
import NotAvailable from '../../../../components/hifi/NotAvailable';
import { API } from '../../../../helpers';

/*
 * v2 admin — Cost Intelligence (Phase 1 savings analyzer).
 *
 * Read-only. Shows how much cost-aware routing WOULD have saved on real
 * historical spend, with a visible heuristic caveat. The headline number is
 * computed server-side from SUM(quota) of real log rows — never a constant —
 * and this page only renders/derives USD from it (USD = quota / quota_per_usd).
 */

const usd = (quota, perUsd) => {
  const d = perUsd > 0 ? quota / perUsd : 0;
  return `$${d.toFixed(2)}`;
};

// Labels/hints are i18n [key, fallback] pairs resolved at render via tr() —
// module scope has no i18n context.
const SCENARIOS = [
  {
    id: 'conservative',
    lk: 'scenario_conservative',
    lf: 'Conservative',
    hk: 'scenario_conservative_hint',
    hf: 'same-family substitute',
  },
  {
    id: 'aggressive',
    lk: 'scenario_aggressive',
    lf: 'Aggressive',
    hk: 'scenario_aggressive_hint',
    hf: 'same-tier cross-vendor',
  },
];

const HFCostIntelligence = () => {
  const { t: tr } = useTranslation();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [scenario, setScenario] = useState('conservative');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setForbidden(false);
    API.get('/api/v2/admin/governance/savings?hours=168', {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (cancelled) return;
        if (res?.data?.success) setData(res.data.data);
      })
      .catch((err) => {
        if (!cancelled && err?.response?.status === 403) setForbidden(true);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const perUsd = data?.quota_per_usd || 500000;
  const active = data?.scenarios?.[scenario] || null;
  const savingsQuota = active?.total_savings_quota || 0;
  const hasSavings = savingsQuota > 0;

  const headline = useMemo(() => {
    if (!hasSavings)
      return tr(
        'console.admin.cost.headline_none',
        '$0.00 — no eligible savings',
      );
    return usd(savingsQuota, perUsd);
  }, [hasSavings, savingsQuota, perUsd, tr]);

  const activeScenario = SCENARIOS.find((s) => s.id === scenario);
  const scenarioLabel = activeScenario
    ? tr(`console.admin.cost.${activeScenario.lk}`, activeScenario.lf)
    : scenario;

  return (
    <HFShell
      active='admin-cost'
      crumbs={[
        tr('console.admin.cost.crumb_admin', 'operations & insights'),
        tr('console.admin.cost.crumb', 'cost intelligence'),
      ]}
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.cost.heading_lbl', 'cost intelligence')}
          </div>
          <h1>{tr('console.admin.cost.title', 'Savings analyzer')}</h1>
          <div className='sub'>
            {tr(
              'console.admin.cost.sub',
              'what cost-aware routing would have saved · last 7 days',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.cost.forbidden_title',
                'Admin access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.cost.forbidden_body',
                'You do not have permission to view platform cost intelligence.',
              )}
            </div>
          </div>
        </div>
      ) : loading ? (
        <div style={{ padding: 28 }} className='muted'>
          {tr('console.common.loading', 'loading…')}
        </div>
      ) : !data ? (
        <div style={{ padding: 28 }}>
          <NotAvailable
            reason={tr(
              'console.admin.cost.no_data_reason',
              'savings endpoint returned no data',
            )}
          />
        </div>
      ) : (
        <div style={{ padding: 28, overflow: 'auto' }}>
          {/* Honest heuristic caveat — always visible, never hidden. */}
          <div
            data-testid='caveat'
            className='panel'
            style={{
              padding: '10px 14px',
              marginBottom: 20,
              fontSize: 11,
              color: 'var(--hf-ink-2)',
              borderLeft: '2px solid var(--hf-accent)',
            }}
          >
            ⚠ {data.caveat}
          </div>

          {/* Scenario toggle */}
          <div style={{ display: 'flex', gap: 8, marginBottom: 18 }}>
            {SCENARIOS.map((s) => (
              <button
                key={s.id}
                type='button'
                data-testid={`scenario-${s.id}`}
                className={'btn sm' + (scenario === s.id ? ' primary' : '')}
                onClick={() => setScenario(s.id)}
                title={tr(`console.admin.cost.${s.hk}`, s.hf)}
              >
                {tr(`console.admin.cost.${s.lk}`, s.lf)}
              </button>
            ))}
          </div>

          {/* Headline */}
          <div
            className='panel'
            style={{ padding: '20px 24px', marginBottom: 24 }}
          >
            <div className='lbl' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.cost.est_savings',
                'estimated savings · {{scenario}}',
                { scenario: scenarioLabel },
              )}
            </div>
            <div
              data-testid='savings-headline'
              className='mono strong'
              style={{ fontSize: 32, lineHeight: 1.1 }}
            >
              {headline}
            </div>
            <div className='muted' style={{ fontSize: 12, marginTop: 6 }}>
              {tr(
                'console.admin.cost.of_total',
                'of {{total}} total spend · {{pct}}% potential',
                {
                  total: usd(data.total_spend_quota, perUsd),
                  pct: ((active?.savings_pct || 0) * 100).toFixed(1),
                },
              )}
            </div>
          </div>

          <div
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr',
              gap: 24,
              marginBottom: 24,
            }}
          >
            <Table
              testid='spend-by-model'
              title={tr('console.admin.cost.spend_by_model', 'spend by model')}
              cols={[
                tr('console.admin.cost.col_model', 'model'),
                tr('console.admin.cost.col_requests', 'requests'),
                tr('console.admin.cost.col_spend', 'spend'),
              ]}
              rows={(data.spend_by_model || []).map((m) => [
                m.model_name,
                String(m.count),
                usd(m.total_quota, perUsd),
              ])}
            />
            <Table
              testid='spend-by-product'
              title={tr(
                'console.admin.cost.spend_by_product',
                'spend by product',
              )}
              cols={[
                tr('console.admin.cost.col_product', 'product'),
                tr('console.admin.cost.col_requests', 'requests'),
                tr('console.admin.cost.col_spend', 'spend'),
              ]}
              rows={(data.spend_by_product || []).map((p) => [
                p.product,
                String(p.count),
                usd(p.total_quota, perUsd),
              ])}
            />
          </div>

          <Table
            testid='top-opportunities'
            title={tr(
              'console.admin.cost.top_opportunities',
              'top opportunities · {{scenario}}',
              { scenario: scenarioLabel },
            )}
            cols={[
              tr('console.admin.cost.col_model', 'model'),
              tr('console.admin.cost.col_alternative', '→ alternative'),
              tr('console.admin.cost.col_est_savings', 'est. savings'),
              '%',
            ]}
            rows={(active?.top_opportunities || []).map((o) => [
              o.model,
              o.alt_model,
              usd(o.savings_quota, perUsd),
              `${(o.savings_pct * 100).toFixed(0)}%`,
            ])}
            empty={tr(
              'console.admin.cost.opportunities_empty',
              'no eligible savings opportunities in this window',
            )}
          />

          {data.excluded && data.excluded.length > 0 && (
            <div style={{ marginTop: 20 }}>
              <div className='lbl' style={{ marginBottom: 8 }}>
                {tr(
                  'console.admin.cost.excluded_lbl',
                  'excluded from re-pricing (honest gaps)',
                )}
              </div>
              <div
                className='muted'
                style={{ fontSize: 11 }}
                data-testid='excluded'
              >
                {data.excluded
                  .map((e) => `${e.model} (${e.reason})`)
                  .join(' · ')}
              </div>
            </div>
          )}
        </div>
      )}
    </HFShell>
  );
};

const cellStyle = {
  padding: '6px 10px',
  borderBottom: '1px solid var(--hf-rule)',
  fontFamily: 'var(--hf-mono)',
  fontSize: 12,
  textAlign: 'left',
};

const Table = ({ testid, title, cols, rows, empty }) => {
  const { t: tr } = useTranslation();
  return (
    <div
      className='panel'
      style={{ padding: '14px 16px' }}
      data-testid={testid}
    >
      <div className='lbl' style={{ marginBottom: 10 }}>
        {title}
      </div>
      {rows.length === 0 ? (
        <div className='muted' style={{ fontSize: 12 }}>
          {empty || tr('console.common.no_data', 'no data')}
        </div>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr>
              {cols.map((c) => (
                <th
                  key={c}
                  style={{
                    ...cellStyle,
                    color: 'var(--hf-ink-3)',
                    fontWeight: 600,
                  }}
                >
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i}>
                {r.map((cell, j) => (
                  <td key={j} style={cellStyle}>
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
};

export default HFCostIntelligence;
