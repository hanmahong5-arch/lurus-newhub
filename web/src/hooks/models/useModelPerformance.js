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

import { useEffect, useState } from 'react';
import { API } from '../../helpers';

/*
 * GET /api/v2/:tenant_slug/models/performance?hours=… — per-model latency
 * and error rate for the caller's tenant (handler/v2_models_performance.go).
 * Members get { model_name, p50_latency_ms, p95_latency_ms, error_rate,
 * enough_samples }; tenant admins also get the volume fields.
 *
 * Decorative for the page that shows it: a failure (older server without
 * the route, 5xx) leaves `items` empty and fires no toast.
 */
export const useModelPerformance = (tenantSlug, { hours = 24 } = {}) => {
  const [items, setItems] = useState([]);
  useEffect(() => {
    if (!tenantSlug) return undefined;
    let cancelled = false;
    API.get(`/api/v2/${tenantSlug}/models/performance?hours=${hours}`, {
      skipErrorHandler: true,
    })
      .then((res) => {
        if (!cancelled && res?.data?.success) {
          setItems(res.data.data?.items || []);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [tenantSlug, hours]);
  return items;
};

export default useModelPerformance;
