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

import React, { useEffect, useState } from 'react';
import {
  Card,
  Spin,
  Tabs,
  Collapsible,
  Typography,
  Banner,
} from '@douyinfe/semi-ui';
import { IconHelpCircle } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

import GroupRatioSettings from '../../pages/Setting/Ratio/GroupRatioSettings';
import ModelRatioSettings from '../../pages/Setting/Ratio/ModelRatioSettings';
import ModelSettingsVisualEditor from '../../pages/Setting/Ratio/ModelSettingsVisualEditor';
import ModelRatioNotSetEditor from '../../pages/Setting/Ratio/ModelRationNotSetEditor';
import UpstreamRatioSync from '../../pages/Setting/Ratio/UpstreamRatioSync';
import FallbackPricingEditor from '../../pages/Setting/Ratio/FallbackPricingEditor';

import { API, showError, toBoolean } from '../../helpers';

const RatioSetting = () => {
  const { t } = useTranslation();

  let [inputs, setInputs] = useState({
    ModelPrice: '',
    ModelRatio: '',
    CacheRatio: '',
    CompletionRatio: '',
    GroupRatio: '',
    GroupGroupRatio: '',
    ImageRatio: '',
    AudioRatio: '',
    AudioCompletionRatio: '',
    AutoGroups: '',
    DefaultUseAutoGroup: false,
    ExposeRatioEnabled: false,
    UserUsableGroups: '',
    'group_ratio_setting.group_special_usable_group': '',
  });

  const [loading, setLoading] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (item.value.startsWith('{') || item.value.startsWith('[')) {
          try {
            item.value = JSON.stringify(JSON.parse(item.value), null, 2);
          } catch (e) {
            // 如果后端返回的不是合法 JSON，直接展示
          }
        }
        if (['DefaultUseAutoGroup', 'ExposeRatioEnabled'].includes(item.key)) {
          newInputs[item.key] = toBoolean(item.value);
        } else {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
    } else {
      showError(message);
    }
  };

  const onRefresh = async () => {
    try {
      setLoading(true);
      await getOptions();
    } catch (error) {
      showError(t('刷新失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    onRefresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [showBillingGuide, setShowBillingGuide] = useState(() => {
    return localStorage.getItem('billing_guide_collapsed') !== 'true';
  });

  const toggleBillingGuide = () => {
    const next = !showBillingGuide;
    setShowBillingGuide(next);
    if (!next) localStorage.setItem('billing_guide_collapsed', 'true');
    else localStorage.removeItem('billing_guide_collapsed');
  };

  return (
    <Spin spinning={loading} size='large'>
      <div
        className='flex items-center gap-2 cursor-pointer select-none mb-2 mt-2 px-1 text-[var(--semi-color-text-2)]'
        onClick={toggleBillingGuide}
      >
        <IconHelpCircle size='small' />
        <Typography.Text type='tertiary' size='small' strong>
          {t('billing_guide_title')}
        </Typography.Text>
        <Typography.Text type='tertiary' size='small'>
          {showBillingGuide ? '▲' : '▼'}
        </Typography.Text>
      </div>
      <Collapsible isOpen={showBillingGuide}>
        <Banner
          type='info'
          bordered
          fullMode={false}
          closeIcon={null}
          className='mb-3'
          description={
            <div className='text-sm leading-relaxed'>
              <div className='font-medium mb-1'>
                {t('billing_guide_formula')}
              </div>
              <ul className='list-disc pl-4 space-y-0.5'>
                <li>{t('billing_guide_model_ratio')}</li>
                <li>{t('billing_guide_completion_ratio')}</li>
                <li>{t('billing_guide_group_ratio')}</li>
                <li>{t('billing_guide_quota')}</li>
              </ul>
            </div>
          }
        />
      </Collapsible>
      {/* 模型倍率设置以及可视化编辑器 */}
      <Card style={{ marginTop: '10px' }}>
        <Tabs type='card'>
          <Tabs.TabPane tab={t('模型倍率设置')} itemKey='model'>
            <ModelRatioSettings options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('分组倍率设置')} itemKey='group'>
            <GroupRatioSettings options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('可视化倍率设置')} itemKey='visual'>
            <ModelSettingsVisualEditor options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('未设置倍率模型')} itemKey='unset_models'>
            <ModelRatioNotSetEditor options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('上游倍率同步')} itemKey='upstream_sync'>
            <UpstreamRatioSync options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
          <Tabs.TabPane tab={t('自动定价')} itemKey='fallback_pricing'>
            <FallbackPricingEditor options={inputs} refresh={onRefresh} />
          </Tabs.TabPane>
        </Tabs>
      </Card>
    </Spin>
  );
};

export default RatioSetting;
