/*
Copyright (C) 2023-2026 QuantumNous

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
import { createContext, useContext } from 'react';
import { useTranslation } from 'react-i18next';

import type { ModelEntry } from '../lib/catalog';
import type { PerfWindow } from '../lib/perf';

/** Window the latency figures were measured over (hours). */
export const PerfWindowContext = createContext<PerfWindow>(24);

export const usePerfWindow = (): PerfWindow => useContext(PerfWindowContext);

/** English source label of a measurement window. */
export function perfWindowLabelKey(w: PerfWindow): string {
  if (w === 1) return 'last hour';
  if (w === 168) return 'last 7 days';
  return 'last 24 hours';
}

/** Why there is no measurement, or null when there is one. */
export function useNoMeasurement(e: ModelEntry): string | null {
  const { t } = useTranslation();
  if (e.p50Ms == null && e.errorRate == null) return t('No traffic yet');
  if (!e.enoughSamples) return t('Too little traffic to measure');
  return null;
}
