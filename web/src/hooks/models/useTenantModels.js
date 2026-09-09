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

import { useCallback, useEffect, useState } from 'react';
import { API } from '../../helpers';

/*
 * One parser for GET /api/v2/:tenant_slug/models, wired against the wire
 * shape ListModelsV2 actually sends (v2_models.go:108-116):
 *   { success, data: { items, total, limit, offset, vendor_counts } }
 *
 * Before this hook existed the fetch was inlined at four call sites in three
 * pages, and one of them read the wire shape wrong: Playground/index.jsx
 * mapped `data` itself as an array — the TypeError died silently in an empty
 * catch, so the swap dropdown never left "loading". Models/index.jsx carried
 * two near-duplicate copies of the correct parser; CommandPalette/index.jsx a
 * third. The lock in no_inline_models_fetch.test.js
 * matches only `API.get(` calls whose literal argument contains `/models`
 * under web/src/pages/v2 — it does not prove this is the only reader, just
 * that no page under that tree re-inlines the fetch.
 */
export const useTenantModels = (
  tenantSlug,
  {
    limit = 100,
    offset = 0,
    vendor = '',
    enabled = true,
    skipErrorHandler = false,
  } = {},
) => {
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [vendorCounts, setVendorCounts] = useState({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  // Bumping this re-runs the effect below without changing any query input —
  // the mechanism refetch() uses.
  const [refetchTick, setRefetchTick] = useState(0);

  const refetch = useCallback(() => setRefetchTick((n) => n + 1), []);

  useEffect(() => {
    if (!enabled || !tenantSlug) return undefined;
    // Cancellation guard — a slug switch or unmount must not let a stale
    // response overwrite state a newer request already set.
    let cancelled = false;
    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        const params = new URLSearchParams({
          limit: String(limit),
          offset: String(offset),
        });
        if (vendor) params.set('vendor', vendor);
        const res = await API.get(
          `/api/v2/${tenantSlug}/models?${params.toString()}`,
          { skipErrorHandler },
        );
        if (cancelled) return;
        if (!res?.data?.success) {
          setItems([]);
          setTotal(0);
          // `||` rather than `??`: an empty-string message is falsy and
          // would leave callers unable to tell a failure from an empty
          // catalogue, which is exactly the confusion this error exists
          // to prevent.
          setError(res?.data?.message || new Error('failed to load models'));
          return;
        }
        // Never treats `data` as an array itself — data is an object whose
        // `items` field is the array.
        const d = res?.data?.data ?? {};
        setItems(Array.isArray(d.items) ? d.items : []);
        setTotal(d.total ?? 0);
        setVendorCounts(d.vendor_counts ?? {});
      } catch (err) {
        if (cancelled) return;
        setItems([]);
        setTotal(0);
        setError(err);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [
    tenantSlug,
    limit,
    offset,
    vendor,
    enabled,
    skipErrorHandler,
    refetchTick,
  ]);

  return { items, total, vendorCounts, loading, error, refetch };
};

export default useTenantModels;
