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
import { RadioGroup, Radio, Tag, Typography } from '@douyinfe/semi-ui';

const { Text } = Typography;

const MODE_FEATURES = {
  external: [
    { key: 'mode_feat_user_mgmt', included: true },
    { key: 'mode_feat_billing', included: true },
    { key: 'mode_feat_multi_token', included: true },
    { key: 'mode_feat_usage_stats', included: true },
  ],
  self: [
    { key: 'mode_feat_simple_ui', included: true },
    { key: 'mode_feat_no_registration', included: false },
    { key: 'mode_feat_no_pricing', included: false },
    { key: 'mode_feat_unified_token', included: true },
  ],
  demo: [
    { key: 'mode_feat_demo_data', included: true },
    { key: 'mode_feat_limited_perms', included: true },
    { key: 'mode_feat_quick_explore', included: true },
  ],
};

const ModeFeatures = ({ mode, t }) => (
  <div className='flex flex-wrap gap-1 mt-1.5'>
    {MODE_FEATURES[mode].map(({ key, included }) => (
      <Tag
        key={key}
        size='small'
        shape='circle'
        color={included ? 'green' : 'grey'}
      >
        {included ? '' : ''} {t(key)}
      </Tag>
    ))}
  </div>
);

const UsageModeStep = ({
  formData,
  handleUsageModeChange,
  renderNavigationButtons,
  t,
}) => {
  return (
    <>
      <RadioGroup
        value={formData.usageMode}
        onChange={handleUsageModeChange}
        type='card'
        direction='horizontal'
        className='mt-4'
        aria-label={t('使用模式选择')}
        name='usage-mode-selection'
      >
        <Radio
          value='external'
          extra={
            <div>
              <div>{t('适用于为多个用户提供服务的场景')}</div>
              <ModeFeatures mode='external' t={t} />
            </div>
          }
          style={{ width: '30%', minWidth: 220 }}
        >
          {t('对外运营模式')}
        </Radio>
        <Radio
          value='self'
          extra={
            <div>
              <div>{t('适用于个人使用的场景，不需要设置模型价格')}</div>
              <ModeFeatures mode='self' t={t} />
            </div>
          }
          style={{ width: '30%', minWidth: 220 }}
        >
          {t('自用模式')}
        </Radio>
        <Radio
          value='demo'
          extra={
            <div>
              <div>{t('适用于展示系统功能的场景，提供基础功能演示')}</div>
              <ModeFeatures mode='demo' t={t} />
            </div>
          }
          style={{ width: '30%', minWidth: 220 }}
        >
          {t('演示站点模式')}
        </Radio>
      </RadioGroup>
      <Text type='tertiary' size='small' className='mt-2 block'>
        {t('mode_changeable_hint')}
      </Text>
      {renderNavigationButtons && renderNavigationButtons()}
    </>
  );
};

export default UsageModeStep;
