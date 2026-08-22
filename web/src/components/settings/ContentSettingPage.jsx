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
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Card, Form, Modal, Spin } from '@douyinfe/semi-ui';
import { API, showError, showSuccess, toBoolean } from '../../helpers';
import { useTranslation } from 'react-i18next';
import SettingsAnnouncements from '../../pages/Setting/Dashboard/SettingsAnnouncements';
import SettingsFAQ from '../../pages/Setting/Dashboard/SettingsFAQ';
import SettingsAPIInfo from '../../pages/Setting/Dashboard/SettingsAPIInfo';

const LEGAL_USER_AGREEMENT_KEY = 'legal.user_agreement';
const LEGAL_PRIVACY_POLICY_KEY = 'legal.privacy_policy';

const ContentSettingPage = () => {
  const { t } = useTranslation();
  const [inputs, setInputs] = useState({
    Notice: '',
    [LEGAL_USER_AGREEMENT_KEY]: '',
    [LEGAL_PRIVACY_POLICY_KEY]: '',
    // Dashboard content
    'console_setting.api_info': '',
    'console_setting.announcements': '',
    'console_setting.faq': '',
    'console_setting.api_info_enabled': '',
    'console_setting.announcements_enabled': '',
    'console_setting.faq_enabled': '',
    // Legacy keys for migration
    ApiInfo: '',
    Announcements: '',
    FAQ: '',
  });
  const [loading, setLoading] = useState(false);
  const [showMigrateModal, setShowMigrateModal] = useState(false);

  const [loadingInput, setLoadingInput] = useState({
    Notice: false,
    [LEGAL_USER_AGREEMENT_KEY]: false,
    [LEGAL_PRIVACY_POLICY_KEY]: false,
  });

  const formApiRef = useRef();

  const updateOption = async (key, value) => {
    const res = await API.put('/api/option/', { key, value });
    const { success, message } = res.data;
    if (success) {
      setInputs((prev) => ({ ...prev, [key]: value }));
    } else {
      showError(message);
    }
  };

  const handleInputChange = async (value, e) => {
    const name = e.target.id;
    setInputs((prev) => ({ ...prev, [name]: value }));
  };

  const submitNotice = async () => {
    try {
      setLoadingInput((s) => ({ ...s, Notice: true }));
      await updateOption('Notice', inputs.Notice);
      showSuccess(t('公告已更新'));
    } catch (error) {
      showError(t('公告更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, Notice: false }));
    }
  };

  const submitUserAgreement = async () => {
    try {
      setLoadingInput((s) => ({ ...s, [LEGAL_USER_AGREEMENT_KEY]: true }));
      await updateOption(
        LEGAL_USER_AGREEMENT_KEY,
        inputs[LEGAL_USER_AGREEMENT_KEY],
      );
      showSuccess(t('用户协议已更新'));
    } catch (error) {
      showError(t('用户协议更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, [LEGAL_USER_AGREEMENT_KEY]: false }));
    }
  };

  const submitPrivacyPolicy = async () => {
    try {
      setLoadingInput((s) => ({ ...s, [LEGAL_PRIVACY_POLICY_KEY]: true }));
      await updateOption(
        LEGAL_PRIVACY_POLICY_KEY,
        inputs[LEGAL_PRIVACY_POLICY_KEY],
      );
      showSuccess(t('隐私政策已更新'));
    } catch (error) {
      showError(t('隐私政策更新失败'));
    } finally {
      setLoadingInput((s) => ({ ...s, [LEGAL_PRIVACY_POLICY_KEY]: false }));
    }
  };

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      const newInputs = {};
      data.forEach((item) => {
        if (item.key in inputs) {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
      if (formApiRef.current) {
        formApiRef.current.setValues(newInputs);
      }
    } else {
      showError(message);
    }
  };

  const refresh = async () => {
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
    refresh();
  }, []);

  const hasLegacyData = useMemo(() => {
    return ['ApiInfo', 'Announcements', 'FAQ'].some((k) => inputs[k]);
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
      showSuccess(t('旧配置迁移完成'));
      await refresh();
      setShowMigrateModal(false);
    } catch (err) {
      showError(t('迁移失败: ') + (err.message || t('未知错误')));
    } finally {
      setLoading(false);
    }
  };

  return (
    <Spin spinning={loading} size='large'>
      <Modal
        title='配置迁移确认'
        visible={showMigrateModal}
        onOk={handleMigrate}
        onCancel={() => setShowMigrateModal(false)}
        confirmLoading={loading}
        okText='确认迁移'
        cancelText='取消'
      >
        <p>检测到旧版本的配置数据，是否要迁移到新的配置格式？</p>
        <p style={{ color: '#f57c00', marginTop: '10px' }}>
          <strong>注意：</strong>
          迁移过程中会自动处理数据格式转换，迁移完成后旧配置将被清除，请在迁移前在数据库中备份好旧配置。
        </p>
      </Modal>

      {/* Legal & notice content */}
      <Form values={inputs} getFormApi={(api) => (formApiRef.current = api)}>
        <Card style={{ marginTop: '10px' }}>
          <Form.Section text={t('通用设置')}>
            <Form.TextArea
              label={t('公告')}
              placeholder={t('在此输入新的公告内容，支持 Markdown & HTML 代码')}
              field='Notice'
              onChange={handleInputChange}
              style={{ fontFamily: 'JetBrains Mono, Consolas' }}
              autosize={{ minRows: 6, maxRows: 12 }}
            />
            <Button onClick={submitNotice} loading={loadingInput['Notice']}>
              {t('设置公告')}
            </Button>
            <Form.TextArea
              label={t('用户协议')}
              placeholder={t('在此输入用户协议内容，支持 Markdown & HTML 代码')}
              field={LEGAL_USER_AGREEMENT_KEY}
              onChange={handleInputChange}
              style={{ fontFamily: 'JetBrains Mono, Consolas' }}
              autosize={{ minRows: 6, maxRows: 12 }}
              helpText={t(
                '填写用户协议内容后，用户注册时将被要求勾选已阅读用户协议',
              )}
            />
            <Button
              onClick={submitUserAgreement}
              loading={loadingInput[LEGAL_USER_AGREEMENT_KEY]}
            >
              {t('设置用户协议')}
            </Button>
            <Form.TextArea
              label={t('隐私政策')}
              placeholder={t('在此输入隐私政策内容，支持 Markdown & HTML 代码')}
              field={LEGAL_PRIVACY_POLICY_KEY}
              onChange={handleInputChange}
              style={{ fontFamily: 'JetBrains Mono, Consolas' }}
              autosize={{ minRows: 6, maxRows: 12 }}
              helpText={t(
                '填写隐私政策内容后，用户注册时将被要求勾选已阅读隐私政策',
              )}
            />
            <Button
              onClick={submitPrivacyPolicy}
              loading={loadingInput[LEGAL_PRIVACY_POLICY_KEY]}
            >
              {t('设置隐私政策')}
            </Button>
          </Form.Section>
        </Card>
      </Form>

      {/* Dashboard content sections */}
      <Card style={{ marginTop: '10px' }}>
        <SettingsAnnouncements options={inputs} refresh={refresh} />
      </Card>
      <Card style={{ marginTop: '10px' }}>
        <SettingsAPIInfo options={inputs} refresh={refresh} />
      </Card>
      <Card style={{ marginTop: '10px' }}>
        <SettingsFAQ options={inputs} refresh={refresh} />
      </Card>
    </Spin>
  );
};

export default ContentSettingPage;
