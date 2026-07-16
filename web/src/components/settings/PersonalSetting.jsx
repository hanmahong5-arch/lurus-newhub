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

import React, { useContext, useEffect, useState } from 'react';
import {
  API,
  copy,
  showError,
  showSuccess,
  setStatusData,
  setUserData,
} from '../../helpers';
import { UserContext } from '../../context/User';
import { useTranslation } from 'react-i18next';

import UserInfoHeader from './personal/components/UserInfoHeader';
import AccountManagement from './personal/cards/AccountManagement';
import NotificationSettings from './personal/cards/NotificationSettings';
import TwoFactorAuth from './personal/cards/TwoFactorAuth';

const PersonalSetting = () => {
  const [userState, userDispatch] = useContext(UserContext);
  const { t } = useTranslation();

  const [status, setStatus] = useState({});
  const [systemToken, setSystemToken] = useState('');
  const [notificationSettings, setNotificationSettings] = useState({
    warningType: 'email',
    warningThreshold: 100000,
    webhookUrl: '',
    webhookSecret: '',
    notificationEmail: '',
    barkUrl: '',
    gotifyUrl: '',
    gotifyToken: '',
    gotifyPriority: 5,
    acceptUnsetModelRatioModel: false,
    recordIpLog: false,
  });

  useEffect(() => {
    let saved = localStorage.getItem('status');
    if (saved) {
      const parsed = JSON.parse(saved);
      setStatus(parsed);
    }
    (async () => {
      try {
        const res = await API.get('/api/status');
        const { success, data } = res.data;
        if (success && data) {
          setStatus(data);
          setStatusData(data);
        }
      } catch (e) {
        // ignore
      }
    })();

    getUserData();
  }, []);

  useEffect(() => {
    if (userState?.user?.setting) {
      const settings = JSON.parse(userState.user.setting);
      setNotificationSettings({
        warningType: settings.notify_type || 'email',
        warningThreshold: settings.quota_warning_threshold || 500000,
        webhookUrl: settings.webhook_url || '',
        webhookSecret: settings.webhook_secret || '',
        notificationEmail: settings.notification_email || '',
        barkUrl: settings.bark_url || '',
        gotifyUrl: settings.gotify_url || '',
        gotifyToken: settings.gotify_token || '',
        gotifyPriority:
          settings.gotify_priority !== undefined ? settings.gotify_priority : 5,
        acceptUnsetModelRatioModel:
          settings.accept_unset_model_ratio_model || false,
        recordIpLog: settings.record_ip_log || false,
      });
    }
  }, [userState?.user?.setting]);

  const generateAccessToken = async () => {
    const res = await API.get('/api/user/token');
    const { success, message, data } = res.data;
    if (success) {
      setSystemToken(data);
      await copy(data);
      showSuccess(t('令牌已重置并已复制到剪贴板'));
    } else {
      showError(message);
    }
  };

  const getUserData = async () => {
    let res = await API.get(`/api/user/self`);
    const { success, message, data } = res.data;
    if (success) {
      userDispatch({ type: 'login', payload: data });
      setUserData(data);
    } else {
      showError(message);
    }
  };

  const handleSystemTokenClick = async (e) => {
    e.target.select();
    await copy(e.target.value);
    showSuccess(t('系统令牌已复制到剪切板'));
  };

  const handleNotificationSettingChange = (type, value) => {
    setNotificationSettings((prev) => ({
      ...prev,
      [type]: value.target
        ? value.target.value !== undefined
          ? value.target.value
          : value.target.checked
        : value,
    }));
  };

  const saveNotificationSettings = async () => {
    try {
      const res = await API.put('/api/user/setting', {
        notify_type: notificationSettings.warningType,
        quota_warning_threshold: parseFloat(
          notificationSettings.warningThreshold,
        ),
        webhook_url: notificationSettings.webhookUrl,
        webhook_secret: notificationSettings.webhookSecret,
        notification_email: notificationSettings.notificationEmail,
        bark_url: notificationSettings.barkUrl,
        gotify_url: notificationSettings.gotifyUrl,
        gotify_token: notificationSettings.gotifyToken,
        gotify_priority: (() => {
          const parsed = parseInt(notificationSettings.gotifyPriority);
          return isNaN(parsed) ? 5 : parsed;
        })(),
        accept_unset_model_ratio_model:
          notificationSettings.acceptUnsetModelRatioModel,
        record_ip_log: notificationSettings.recordIpLog,
      });

      if (res.data.success) {
        showSuccess(t('设置保存成功'));
        await getUserData();
      } else {
        showError(res.data.message);
      }
    } catch (error) {
      showError(t('设置保存失败'));
    }
  };

  return (
    <div>
      <div className='flex justify-center'>
        <div className='w-full max-w-7xl mx-auto px-2'>
          <UserInfoHeader t={t} userState={userState} />

          <div className='grid grid-cols-1 xl:grid-cols-2 items-start gap-4 md:gap-6 mt-4 md:mt-6'>
            <AccountManagement
              t={t}
              userState={userState}
              status={status}
              systemToken={systemToken}
              generateAccessToken={generateAccessToken}
              handleSystemTokenClick={handleSystemTokenClick}
            />

            <NotificationSettings
              t={t}
              notificationSettings={notificationSettings}
              handleNotificationSettingChange={handleNotificationSettingChange}
              saveNotificationSettings={saveNotificationSettings}
            />

            <TwoFactorAuth t={t} />
          </div>
        </div>
      </div>
    </div>
  );
};

export default PersonalSetting;
