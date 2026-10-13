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
import type { CatalogFilter, PriceType } from './catalog';

/** Sidebar selections. Empty list / empty string = "all". */
export interface Filters {
  vendors: string[];
  capabilities: string[];
  tags: string[];
  modalities: string[];
  priceType: PriceType | '';
  routableOnly: boolean;
}

export const EMPTY_FILTERS: Filters = {
  vendors: [],
  capabilities: [],
  tags: [],
  modalities: [],
  priceType: '',
  routableOnly: false,
};

/** How many facets are narrowing the list (the search box is not one). */
export function activeFilterCount(f: Filters, group: string): number {
  return (
    f.vendors.length +
    f.capabilities.length +
    f.tags.length +
    f.modalities.length +
    (f.priceType ? 1 : 0) +
    (f.routableOnly ? 1 : 0) +
    (group ? 1 : 0)
  );
}

export function toCatalogFilter(
  q: string,
  f: Filters,
  group: string,
): CatalogFilter {
  return {
    q,
    vendors: f.vendors,
    capabilities: f.capabilities,
    tags: f.tags,
    modalities: f.modalities,
    priceType: f.priceType,
    routableOnly: f.routableOnly,
    group,
  };
}

export const toggleIn = (list: string[], v: string): string[] =>
  list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
