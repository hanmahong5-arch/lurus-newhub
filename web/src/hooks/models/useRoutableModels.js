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

// The two wires the console builds example snippets for; supported_endpoint_types
// can carry others (see internal/pkg/constant/endpoint_type.go).
export const WIRE_OPENAI = 'openai';
export const WIRE_ANTHROPIC = 'anthropic';

/*
 * One parser for GET /api/v2/:tenant_slug/models/routable, wired against
 * ListRoutableModelsV2's wire shape (internal/adapter/handler/
 * v2_models_routable.go): { success, data: { items } }, items always an
 * array (never null, even when empty).
 *
 * This is a DIFFERENT endpoint from useTenantModels' GET .../models — that
 * one reads the manual catalogue table and cannot answer "what can this
 * tenant route"; this one is the routing-truth list the four console pages
 * (Chat, Playground, Dashboard, Token) now build their model pickers and
 * example snippets from instead of a hardcoded vendor model name.
 */
export const useRoutableModels = (
  tenantSlug,
  { enabled = true, skipErrorHandler = false } = {},
) => {
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(null);
  // resolved distinguishes "have not asked yet / still in flight" (render
  // nothing definitive) from "asked, and the truthful answer is N models"
  // (N may be zero) — callers must not flash an honest-empty-state on every
  // page load before the first response has actually come back.
  const [resolved, setResolved] = useState(false);
  const [refetchTick, setRefetchTick] = useState(0);

  const refetch = useCallback(() => setRefetchTick((n) => n + 1), []);

  useEffect(() => {
    if (!enabled || !tenantSlug) return undefined;
    let cancelled = false;
    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        const res = await API.get(`/api/v2/${tenantSlug}/models/routable`, {
          skipErrorHandler,
        });
        if (cancelled) return;
        if (!res?.data?.success) {
          setItems([]);
          // `||` rather than `??`: an empty-string message is falsy and
          // would leave callers unable to tell a failure from an empty
          // (but successfully resolved) catalogue.
          setError(
            res?.data?.message || new Error('failed to load routable models'),
          );
          setResolved(true);
          return;
        }
        const d = res?.data?.data ?? {};
        setItems(Array.isArray(d.items) ? d.items : []);
        setResolved(true);
      } catch (err) {
        if (cancelled) return;
        setItems([]);
        setError(err);
        setResolved(true);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    run();
    return () => {
      cancelled = true;
    };
  }, [tenantSlug, enabled, skipErrorHandler, refetchTick]);

  return { items, loading, error, resolved, refetch };
};

// routableFor narrows `items` to those whose supported_endpoint_types
// includes `wire`. No wire given -> every item (used by pages that don't
// care which wire, e.g. the Playground swap list).
export function routableFor(items, wire) {
  const list = Array.isArray(items) ? items : [];
  if (!wire) return list;
  return list.filter(
    (m) =>
      Array.isArray(m?.supported_endpoint_types) &&
      m.supported_endpoint_types.includes(wire),
  );
}

// firstRoutableModel is routableFor(items, wire)[0], or null when nothing
// routable speaks that wire.
export function firstRoutableModel(items, wire) {
  const list = routableFor(items, wire);
  return list.length > 0 ? list[0] : null;
}

// defaultCompareModels takes the first `max` routable models — the
// Playground's initial compare-draft when nothing has been picked yet.
export function defaultCompareModels(items, max = 3) {
  const list = Array.isArray(items) ? items : [];
  return list.slice(0, max);
}

// intersectTokenLimits narrows `items` to a token's own model_limits CSV
// (entity/token.go's ModelLimitsEnabled/ModelLimits) when that token
// restricts its models; a token with no restriction sees the full routable
// set unchanged.
export function intersectTokenLimits(items, token) {
  const list = Array.isArray(items) ? items : [];
  if (!token || !token.model_limits_enabled || !token.model_limits) {
    return list;
  }
  const allowed = new Set(
    String(token.model_limits)
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean),
  );
  if (allowed.size === 0) return list;
  return list.filter((m) => allowed.has(m?.id));
}

export default useRoutableModels;
