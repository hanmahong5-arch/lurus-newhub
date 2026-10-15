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
import type { TFunction } from 'i18next'

import { MAX_TIERS, type ValidationCode } from '../lib/pricing'

/** Human text of a client-side validation code. */
export function validationText(code: ValidationCode, t: TFunction): string {
  switch (code) {
    case 'positive':
      return t('Enter a number greater than 0, or leave it blank.')
    case 'threshold':
      return t('The threshold must be a whole number of tokens, 0 or more.')
    case 'ascending':
      return t('Thresholds must be strictly ascending.')
    case 'too-many-tiers':
      return t('At most {{max}} tiers per model.', { max: MAX_TIERS })
  }
}
