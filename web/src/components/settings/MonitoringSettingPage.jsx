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
import React, { useEffect, useMemo, useState } from 'react';
import { Card, Spin, Button, Modal } from '@douyinfe/semi-ui';
import { API, showError, showSuccess, toBoolean } from '../../helpers';
import SettingsMonitoring from '../../pages/Setting/Operation/SettingsMonitoring';
import SettingsLog from '../../pages/Setting/Operation/SettingsLog';
import SettingsDataDashboard from '../../pages/Setting/Dashboard/SettingsDataDashboard';
import SettingsUptimeKuma from '../../pages/Setting/Dashboard/SettingsUptimeKuma';
import i18next from '../../i18n/i18n';

const MonitoringSettingPage = () => {
  const [inputs, setInputs] = useState({
    // Operation monitoring
    ChannelDisableThreshold: 0,
    QuotaRemindThreshold: 0,
    AutomaticDisableChannelEnabled: false,
    AutomaticEnableChannelEnabled: false,
    AutomaticDisableKeywords: '',
    'monitor_setting.auto_test_channel_enabled': false,
    'monitor_setting.auto_test_channel_minutes': 10,
    // Operation log
    LogConsumeEnabled: false,
    // Dashboard
    'console_setting.uptime_kuma_groups': '',
    'console_setting.uptime_kuma_enabled': '',
    DataExportEnabled: false,
    DataExportDefaultTime: 'hour',
    DataExportInterval: 5,
    // Legacy keys for migration detection
    UptimeKumaUrl: '',
    UptimeKumaSlug: '',
  });

  const [loading, setLoading] = useState(false);
  const [showMigrateModal, setShowMigrateModal] = useState(false);

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      const newInputs = {};
      data.forEach((item) => {
        if (typeof inputs[item.key] === 'boolean') {
          newInputs[item.key] = toBoolean(item.value);
        } else if (item.key in inputs) {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
    } else {
      showError(message);
    }
  };

  const refresh = async () => {
    try {
      setLoading(true);
      await getOptions();
    } catch (error) {
      showError(i18next.t('刷新失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const hasLegacyData = useMemo(() => {
    return ['UptimeKumaUrl', 'UptimeKumaSlug'].some((k) => inputs[k]);
  }, [inputs]);

  useEffect(() => {
    if (hasLegacyData) {
      setShowMigrateModal(true);
    }
  }, [hasLegacyData]);

  const handleMigrate = async () => {
    try {
      setLoading(true);
      await API.post('/api/option/migrate_console_setting');
      showSuccess(i18next.t('旧配置迁移完成'));
      await refresh();
      setShowMigrateModal(false);
    } catch (err) {
      console.error(err);
      showError(
        i18next.t('迁移失败: ') + (err.message || i18next.t('未知错误')),
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <Spin spinning={loading} size='large'>
      <Modal
        title={i18next.t('配置迁移确认')}
        visible={showMigrateModal}
        onOk={handleMigrate}
        onCancel={() => setShowMigrateModal(false)}
        confirmLoading={loading}
        okText={i18next.t('确认迁移')}
        cancelText={i18next.t('取消')}
      >
        <p>
          {i18next.t(
            '检测到旧版本的 UptimeKuma 配置数据，是否要迁移到新的配置格式？',
          )}
        </p>
      </Modal>

      <Card style={{ marginTop: '10px' }}>
        <SettingsMonitoring options={inputs} refresh={refresh} />
      </Card>
      <Card style={{ marginTop: '10px' }}>
        <SettingsLog options={inputs} refresh={refresh} />
      </Card>
      <Card style={{ marginTop: '10px' }}>
        <SettingsDataDashboard options={inputs} refresh={refresh} />
      </Card>
      <Card style={{ marginTop: '10px' }}>
        <SettingsUptimeKuma options={inputs} refresh={refresh} />
      </Card>
    </Spin>
  );
};

export default MonitoringSettingPage;
