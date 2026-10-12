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

import React from 'react';

// 格式化价格信息（用于卡片视图）
export const formatPriceInfo = (priceData, t) => {
  if (priceData.unpriced) {
    return (
      <span style={{ color: 'var(--semi-color-text-2)' }}>
        {t('未配置定价')}
      </span>
    );
  }

  if (priceData.isPerToken) {
    return (
      <>
        <span style={{ color: 'var(--semi-color-text-1)' }}>
          {t('输入')} {priceData.inputPrice}/{priceData.unitLabel}
        </span>
        <span style={{ color: 'var(--semi-color-text-1)' }}>
          {t('输出')} {priceData.completionPrice}/{priceData.unitLabel}
        </span>
      </>
    );
  }

  return (
    <>
      <span style={{ color: 'var(--semi-color-text-1)' }}>
        {t('模型价格')} {priceData.price}
      </span>
    </>
  );
};
