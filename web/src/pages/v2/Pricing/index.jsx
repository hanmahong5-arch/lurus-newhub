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
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import { API, showError, showSuccess } from '../../../helpers';
import useFormDraft from '../../../hooks/common/useFormDraft';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* v2 Pricing — GET /api/v2/:tenant_slug/pricing (2026-05-19)
   Write path — POST /api/v2/:tenant_slug/pricing (Epic 12, 2026-05-20).
   Optimistic lock + preview (L1, 2026-09-12): GET returns data.version; POST
   sends it back as If-Match-Pricing-Version — a stale header gets 409 (the
   PricingVersion row the server compares against) and this page refetches
   instead of saving. The header only guards the version counter, not the
   underlying ratio maps a concurrent writer on another replica may have
   changed for a different model (see v2_pricing_write.go's UpdatePricingV2
   comment). Preview runs the same batch read-only first. */

const DRAFT_KEY = 'v2-pricing-edits';

const PricingPage = () => {
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` per the v2 console convention (avoids shadowing).
  const { t: tr } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [pricing, setPricing] = useState([]);
  const [vendors, setVendors] = useState([]);
  const [groupRatio, setGroupRatio] = useState({});
  const [vendorFilter, setVendorFilter] = useState('');
  // PricingVersion read from the last GET; sent back on the next POST.
  const [version, setVersion] = useState(0);
  // Diff rows from the last preview call; null until Preview is clicked.
  // handleSave and handleFieldChange both clear it (on a successful save, or
  // on any further edit) so a stale preview cannot linger, but neither path
  // has a test asserting the clear — see the lane report.
  const [previewDiffs, setPreviewDiffs] = useState(null);
  // fetchTick increments trigger a re-fetch without remounting.
  const [fetchTick, setFetchTick] = useState(0);

  // Map of model_name → edited fields. Draft persists across page refresh.
  const [edits, setEdits, clearEdits, isDirty] = useFormDraft(
    DRAFT_KEY,
    {},
    { schemaVersion: 1 },
  );

  useEffect(() => {
    if (!tenantSlug) return;
    setLoading(true);
    API.get(`/api/v2/${tenantSlug}/pricing`)
      .then((res) => {
        const d = res?.data?.data ?? {};
        setPricing(Array.isArray(d.pricing) ? d.pricing : []);
        setVendors(Array.isArray(d.vendors) ? d.vendors : []);
        setGroupRatio(
          d.group_ratio && typeof d.group_ratio === 'object'
            ? d.group_ratio
            : {},
        );
        setVersion(typeof d.version === 'number' ? d.version : 0);
      })
      .catch((err) => {
        const msg =
          err?.response?.data?.message ??
          tr('console.pricing.load_failed', 'Failed to load pricing data');
        showError(msg);
      })
      .finally(() => setLoading(false));
    // `tr` intentionally omitted: its identity is not stable under the test
    // i18n mock and would re-trigger the fetch on every render; the fetch
    // must run only on slug change / explicit refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tenantSlug, fetchTick]);

  // Merge server rows with in-progress edits for display.
  const displayPricing = useMemo(
    () =>
      pricing.map((row) => {
        const e = edits[row.model_name];
        if (!e) return row;
        return { ...row, ...e };
      }),
    [pricing, edits],
  );

  const filteredPricing = useMemo(() => {
    if (!vendorFilter) return displayPricing;
    return displayPricing.filter((p) => p.vendor === vendorFilter);
  }, [displayPricing, vendorFilter]);

  const refreshList = useCallback(() => setFetchTick((n) => n + 1), []);

  const handleFieldChange = (modelName, field, value) => {
    setEdits((prev) => ({
      ...prev,
      [modelName]: { ...(prev[modelName] ?? {}), [field]: value },
    }));
    // Any further edit invalidates the diff shown by the last Preview.
    setPreviewDiffs(null);
  };

  // Build the batch: only send changed rows with their model_name. Shared by
  // Save and Preview so both send the same request body — the server still
  // computes preview's diff against its own baseline, which can differ from
  // what the write applies (see UpdatePricingV2's comment).
  const buildBatch = () =>
    Object.entries(edits)
      .map(([modelName, fields]) => {
        const item = { model_name: modelName };
        if (fields.model_ratio !== undefined)
          item.model_ratio = parseFloat(fields.model_ratio);
        if (fields.completion_ratio !== undefined)
          item.completion_ratio = parseFloat(fields.completion_ratio);
        if (fields.model_price !== undefined)
          item.model_price = parseFloat(fields.model_price);
        if (fields.cache_ratio !== undefined)
          item.cache_ratio = parseFloat(fields.cache_ratio);
        return item;
      })
      .filter((item) => Object.keys(item).length > 1); // skip items with only model_name

  const handlePreview = async () => {
    const batch = buildBatch();
    if (batch.length === 0) return;

    setPreviewing(true);
    try {
      const res = await API.post(
        `/api/v2/${tenantSlug}/pricing/preview`,
        batch,
      );
      setPreviewDiffs(
        Array.isArray(res?.data?.data?.diffs) ? res.data.data.diffs : [],
      );
    } catch (err) {
      const msg =
        err?.response?.data?.message ??
        tr('console.pricing.preview_failed', 'Failed to preview pricing');
      showError(msg);
    } finally {
      setPreviewing(false);
    }
  };

  const handleSave = async () => {
    if (!isDirty) return;

    const batch = buildBatch();
    if (batch.length === 0) return;

    setSaving(true);
    try {
      const res = await API.post(`/api/v2/${tenantSlug}/pricing`, batch, {
        headers: { 'If-Match-Pricing-Version': String(version) },
      });
      const count = res?.data?.data?.updated_count ?? batch.length;
      showSuccess(tr('console.pricing.toast_saved', { count }));
      clearEdits();
      setPreviewDiffs(null);
      refreshList();
    } catch (err) {
      if (err?.response?.status === 409) {
        showError(
          tr(
            'console.pricing.version_conflict',
            'Pricing changed since you last loaded it — refreshing',
          ),
        );
        refreshList();
      } else {
        const msg =
          err?.response?.data?.message ??
          tr('console.pricing.save_failed', 'Failed to save pricing');
        showError(msg);
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <HFShell
      active='pricing'
      crumbs={[
        tr('console.pricing.crumb_section', 'platform'),
        tr('console.pricing.crumb', 'pricing'),
      ]}
      actions={
        <>
          {loading && (
            <span className='muted mono' style={{ fontSize: 11 }}>
              {tr('console.common.loading', 'loading…')}
            </span>
          )}
          <button
            type='button'
            className='btn'
            disabled={!isDirty || previewing}
            data-testid='pricing-preview'
            onClick={handlePreview}
          >
            {previewing
              ? tr('console.pricing.previewing', 'previewing…')
              : tr('console.pricing.preview_btn', 'preview')}
          </button>
          <button
            type='button'
            className='btn primary'
            disabled={!isDirty || saving}
            data-testid='pricing-save'
            onClick={handleSave}
          >
            {saving
              ? tr('console.pricing.saving', 'saving…')
              : tr('console.common.save', 'save')}
          </button>
        </>
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.pricing.title', 'pricing management')}
          </div>
          <h1>{tr('console.pricing.heading', 'Model pricing')}</h1>
          <div className='sub'>
            {tr(
              'console.pricing.subtitle',
              'vendor cost · group ratios · billing type',
            )}
          </div>
        </div>
      </div>

      {/* Vendor filter toolbar */}
      <div
        style={{
          padding: '0 24px 12px',
          display: 'flex',
          gap: 8,
          flexWrap: 'wrap',
        }}
      >
        <button
          type='button'
          className={`btn${!vendorFilter ? ' primary' : ''}`}
          onClick={() => setVendorFilter('')}
        >
          {tr('console.pricing.all_vendors', 'all')}
        </button>
        {vendors.map((v) => (
          <button
            key={v}
            type='button'
            className={`btn${vendorFilter === v ? ' primary' : ''}`}
            onClick={() => setVendorFilter(v)}
          >
            {v}
          </button>
        ))}
      </div>

      <div style={{ padding: '0 24px 24px' }}>
        <div className='panel'>
          <div
            style={{
              padding: '14px 18px',
              borderBottom: '1px solid var(--hf-rule)',
              display: 'flex',
              alignItems: 'baseline',
            }}
          >
            <div className='lbl'>
              {tr('console.pricing.model_list', 'model list')}
            </div>
            <span
              className='muted mono'
              style={{ fontSize: 10, marginLeft: 'auto' }}
            >
              {tr('console.pricing.model_count', {
                count: filteredPricing.length,
              })}
            </span>
          </div>
          <div className='hf-table-scroll'>
            <table className='t' data-testid='pricing-table'>
              <thead>
                <tr>
                  <th>{tr('console.pricing.th_model_name', 'model name')}</th>
                  <th>{tr('console.pricing.th_vendor', 'vendor')}</th>
                  <th>{tr('console.pricing.th_quota_type', 'billing type')}</th>
                  <th>{tr('console.pricing.th_model_ratio', 'model ratio')}</th>
                  <th>
                    {tr(
                      'console.pricing.th_completion_ratio',
                      'completion ratio',
                    )}
                  </th>
                  <th>{tr('console.pricing.th_model_price', 'model price')}</th>
                  <th>{tr('console.pricing.th_cache_ratio', 'cache ratio')}</th>
                  <th>
                    {tr('console.pricing.th_enable_groups', 'enabled groups')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {filteredPricing.map((row, i) => (
                  <tr key={row.model_name ?? i}>
                    <td className='strong mono' style={{ fontSize: 12 }}>
                      {row.model_name}
                    </td>
                    <td>{row.vendor ?? '—'}</td>
                    <td>
                      <span className='tag'>
                        {row.quota_type === 1
                          ? tr(
                              'console.pricing.quota_type_price',
                              'per-call price',
                            )
                          : tr(
                              'console.pricing.quota_type_ratio',
                              'ratio-based',
                            )}
                      </span>
                    </td>
                    <td>
                      {row.quota_type === 0 ? (
                        <input
                          type='number'
                          className='field'
                          step='0.0001'
                          min='0.0001'
                          value={
                            edits[row.model_name]?.model_ratio ??
                            row.model_ratio ??
                            ''
                          }
                          onChange={(e) =>
                            handleFieldChange(
                              row.model_name,
                              'model_ratio',
                              e.target.value,
                            )
                          }
                          style={{ width: 90, height: 24, fontSize: 11 }}
                          data-testid={`field-model_ratio-${row.model_name}`}
                        />
                      ) : (
                        <span className='mono muted'>—</span>
                      )}
                    </td>
                    <td>
                      {row.quota_type === 0 ? (
                        <input
                          type='number'
                          className='field'
                          step='0.0001'
                          min='0.0001'
                          value={
                            edits[row.model_name]?.completion_ratio ??
                            row.completion_ratio ??
                            ''
                          }
                          onChange={(e) =>
                            handleFieldChange(
                              row.model_name,
                              'completion_ratio',
                              e.target.value,
                            )
                          }
                          style={{ width: 90, height: 24, fontSize: 11 }}
                          data-testid={`field-completion_ratio-${row.model_name}`}
                        />
                      ) : (
                        <span className='mono muted'>—</span>
                      )}
                    </td>
                    <td>
                      {row.quota_type === 1 ? (
                        <input
                          type='number'
                          className='field'
                          step='0.000001'
                          min='0.000001'
                          value={
                            edits[row.model_name]?.model_price ??
                            row.model_price ??
                            ''
                          }
                          onChange={(e) =>
                            handleFieldChange(
                              row.model_name,
                              'model_price',
                              e.target.value,
                            )
                          }
                          style={{ width: 90, height: 24, fontSize: 11 }}
                          data-testid={`field-model_price-${row.model_name}`}
                        />
                      ) : (
                        <span className='mono muted'>—</span>
                      )}
                    </td>
                    <td>
                      {/* GET pricing (v2_pricing.go) projects the model's
                          current cache_ratio when one is explicitly
                          configured, so this prefills from row.cache_ratio
                          like the other three fields; a model with no
                          configured entry starts blank instead of showing a
                          fabricated value. */}
                      <input
                        type='number'
                        className='field'
                        step='0.0001'
                        min='0.0001'
                        value={
                          edits[row.model_name]?.cache_ratio ??
                          row.cache_ratio ??
                          ''
                        }
                        onChange={(e) =>
                          handleFieldChange(
                            row.model_name,
                            'cache_ratio',
                            e.target.value,
                          )
                        }
                        style={{ width: 90, height: 24, fontSize: 11 }}
                        data-testid={`field-cache_ratio-${row.model_name}`}
                      />
                    </td>
                    <td>
                      <span className='muted' style={{ fontSize: 11 }}>
                        {Array.isArray(row.enable_groups)
                          ? row.enable_groups.join(', ') || '—'
                          : '—'}
                      </span>
                    </td>
                  </tr>
                ))}
                {filteredPricing.length === 0 && !loading && (
                  <tr>
                    <td
                      colSpan={8}
                      className='muted'
                      style={{ textAlign: 'center', padding: 24 }}
                    >
                      {tr('console.common.no_data', 'no data')}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>

        {/* Preview diff — dry-run of the pending edits, never persisted. */}
        {Array.isArray(previewDiffs) && (
          <div className='panel' style={{ marginTop: 18, padding: 18 }}>
            <div className='lbl' style={{ marginBottom: 10 }}>
              {tr('console.pricing.preview_heading', 'preview diff')}
            </div>
            {previewDiffs.length === 0 ? (
              <div className='muted' style={{ fontSize: 12 }}>
                {tr('console.pricing.preview_empty', 'No changes to preview.')}
              </div>
            ) : (
              <table className='t' data-testid='pricing-preview-diff'>
                <thead>
                  <tr>
                    <th>{tr('console.pricing.th_model_name', 'model name')}</th>
                    <th>{tr('console.pricing.preview_field', 'field')}</th>
                    <th>{tr('console.pricing.preview_old', 'old')}</th>
                    <th>{tr('console.pricing.preview_new', 'new')}</th>
                  </tr>
                </thead>
                <tbody>
                  {previewDiffs.map((d, i) => (
                    <tr key={`${d.model_name}-${d.field}-${i}`}>
                      <td className='strong mono' style={{ fontSize: 12 }}>
                        {d.model_name}
                      </td>
                      <td className='mono' style={{ fontSize: 11 }}>
                        {d.field}
                      </td>
                      <td className='mono muted' style={{ fontSize: 11 }}>
                        {d.old}
                      </td>
                      <td className='mono' style={{ fontSize: 11 }}>
                        {d.new}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}

        {/* Group ratio — readonly display */}
        {Object.keys(groupRatio).length > 0 && (
          <div className='panel' style={{ marginTop: 18, padding: 18 }}>
            <div className='lbl' style={{ marginBottom: 10 }}>
              {tr(
                'console.pricing.group_ratio_readonly',
                'group ratios (read-only)',
              )}
            </div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
              {Object.entries(groupRatio).map(([group, ratio]) => (
                <div
                  key={group}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    alignItems: 'center',
                    padding: '8px 14px',
                    border: '1px solid var(--hf-rule)',
                    borderRadius: 4,
                    minWidth: 80,
                  }}
                >
                  <span className='tag' style={{ marginBottom: 4 }}>
                    {group}
                  </span>
                  <span className='display mono' style={{ fontSize: 16 }}>
                    ×{Number(ratio).toFixed(2)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </HFShell>
  );
};

export default PricingPage;
