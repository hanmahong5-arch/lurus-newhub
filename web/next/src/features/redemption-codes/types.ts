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
/** A row of GET /api/v2/~/redemptions (handler.redemptionView). */
export interface Redemption {
  id: number
  name: string
  /** Enabled codes come back masked ("abcd****...wxyz"); the full code is only returned once, at creation. */
  key: string
  /** 1 = unused, 2 = disabled, 3 = used. */
  status: number
  quota: number
  created_time: number
  /** Unix seconds; 0 = never expires. */
  expired_time: number
  redeemed_time: number
  used_user_id: number
}

export interface RedemptionPage {
  redemptions: Redemption[] | null
  total: number
  page: number
  page_size: number
}

export interface RedemptionListParams {
  page: number
  pageSize: number
  keyword: string
}

export interface CreateRedemptionBody {
  name: string
  quota: number
  count: number
  expired_time: number
}

export interface CreatedCode {
  id: number
  key: string
  name: string
}

/** POST /api/v2/~/redemptions answers with whatever could be generated. */
export interface CreatedCodes {
  codes: CreatedCode[]
  count: number
  requested: number
}

export interface UpdateRedemptionBody {
  id: number
  name: string
  quota: number
  expired_time: number
}

/** Plaintext codes of one just-created batch, held only in memory. */
export interface CreatedBatch {
  keys: string[]
  name: string
  quota: number
  requested: number
}

export type RedemptionDialog =
  | { kind: 'create' }
  | { kind: 'edit'; redemption: Redemption }
  | { kind: 'delete'; redemption: Redemption }
  | { kind: 'delete-invalid' }
  | { kind: 'created'; batch: CreatedBatch }
  | null
