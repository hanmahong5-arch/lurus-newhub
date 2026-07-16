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

import React, { useCallback, useEffect, useState } from 'react';
import {
  Avatar,
  Banner,
  Button,
  Card,
  Input,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { ShieldCheck } from 'lucide-react';
import {
  TotpService,
  createApiCalls,
} from '../../../../services/secureVerification';
import { useSecureVerification } from '../../../../hooks/common/useSecureVerification';
import SecureVerificationModal from '../../../common/modals/SecureVerificationModal';
import { copy, showError, showSuccess } from '../../../../helpers';

/**
 * TOTP (两步验证) enrollment card for personal settings.
 * Enroll → show secret + otpauth URL for manual authenticator entry →
 * confirm with one code → active. Disable goes through the step-up
 * verification flow (enrolled users must present a TOTP code).
 */
const TwoFactorAuth = ({ t }) => {
  const [status, setStatus] = useState({ enrolled: false, pending: false });
  const [enrollment, setEnrollment] = useState(null); // { secret, otpauth_url }
  const [confirmCode, setConfirmCode] = useState('');
  const [loading, setLoading] = useState(false);

  const refreshStatus = useCallback(async () => {
    try {
      const data = await TotpService.getStatus();
      setStatus(data || { enrolled: false, pending: false });
    } catch (e) {
      // Leave the previous state; the card stays usable.
    }
  }, []);

  useEffect(() => {
    refreshStatus();
  }, [refreshStatus]);

  const {
    isModalVisible,
    verificationMethods,
    verificationState,
    startVerification,
    executeVerification,
    cancelVerification,
    setVerificationCode,
    switchVerificationMethod,
  } = useSecureVerification({
    onSuccess: async (result) => {
      if (result?.success) {
        showSuccess(t('两步验证已禁用'));
        setEnrollment(null);
        setConfirmCode('');
        await refreshStatus();
      } else if (result) {
        showError(result.message || t('操作失败'));
      }
    },
  });

  const handleEnroll = async () => {
    setLoading(true);
    try {
      const data = await TotpService.enroll();
      setEnrollment(data);
      setConfirmCode('');
      await refreshStatus();
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  const handleConfirm = async () => {
    if (!confirmCode.trim()) {
      showError(t('请输入验证码'));
      return;
    }
    setLoading(true);
    try {
      await TotpService.confirm(confirmCode);
      showSuccess(t('两步验证启用成功！'));
      setEnrollment(null);
      setConfirmCode('');
      await refreshStatus();
    } catch (e) {
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  const handleDisable = async () => {
    // Disable is gated by step-up verification: the modal collects the TOTP
    // code, POST /api/verify stamps the session, then the disable call runs.
    await startVerification(
      createApiCalls.custom('/api/user/totp/disable', 'POST'),
      {
        title: t('禁用两步验证'),
        description: t('禁用前需要先完成一次安全验证。'),
      },
    );
  };

  const handleCopy = async (value) => {
    await copy(value);
    showSuccess(t('已复制到剪贴板'));
  };

  return (
    <Card className='!rounded-2xl'>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='green' className='mr-3 shadow-md'>
          <ShieldCheck size={16} />
        </Avatar>
        <div>
          <Typography.Text className='text-lg font-medium'>
            {t('两步验证')}
          </Typography.Text>
          <div className='text-xs text-gray-600'>
            {t('敏感操作需要输入验证器应用生成的动态验证码')}
          </div>
        </div>
        <div className='ml-auto'>
          {status.enrolled ? (
            <Tag color='green'>{t('已启用')}</Tag>
          ) : (
            <Tag color='grey'>{t('未启用')}</Tag>
          )}
        </div>
      </div>

      <div className='py-2'>
        {status.enrolled ? (
          <div className='flex flex-col gap-3'>
            <Typography.Text type='tertiary' className='text-sm'>
              {t(
                '两步验证已启用。执行敏感操作时需要输入验证器应用中的验证码。',
              )}
            </Typography.Text>
            <div>
              <Button type='danger' theme='solid' onClick={handleDisable}>
                {t('禁用两步验证')}
              </Button>
            </div>
          </div>
        ) : enrollment ? (
          <div className='flex flex-col gap-3'>
            <Banner
              type='warning'
              closeIcon={null}
              description={t(
                '密钥仅显示一次，请立即添加到验证器应用（如 Google Authenticator）。',
              )}
            />
            <div>
              <div className='text-xs text-gray-500 mb-1'>{t('密钥')}</div>
              <Input
                readonly
                value={enrollment.secret}
                onClick={() => handleCopy(enrollment.secret)}
              />
            </div>
            <div>
              <div className='text-xs text-gray-500 mb-1'>
                {t('otpauth 链接（可在验证器应用中导入）')}
              </div>
              <Input
                readonly
                value={enrollment.otpauth_url}
                onClick={() => handleCopy(enrollment.otpauth_url)}
              />
            </div>
            <div>
              <div className='text-xs text-gray-500 mb-1'>
                {t('输入验证器应用生成的6位验证码以完成启用')}
              </div>
              <div className='flex gap-2'>
                <Input
                  value={confirmCode}
                  onChange={setConfirmCode}
                  maxLength={6}
                  placeholder={t('请输入6位验证码')}
                  style={{ maxWidth: 220 }}
                />
                <Button
                  type='primary'
                  theme='solid'
                  loading={loading}
                  onClick={handleConfirm}
                >
                  {t('确认启用')}
                </Button>
              </div>
            </div>
          </div>
        ) : (
          <div className='flex flex-col gap-3'>
            <Typography.Text type='tertiary' className='text-sm'>
              {t(
                '启用后，查看渠道密钥等敏感操作将要求输入验证器应用生成的验证码。',
              )}
            </Typography.Text>
            <div>
              <Button
                type='primary'
                theme='solid'
                loading={loading}
                onClick={handleEnroll}
              >
                {status.pending ? t('重新生成密钥') : t('启用两步验证')}
              </Button>
            </div>
          </div>
        )}
      </div>

      <SecureVerificationModal
        visible={isModalVisible}
        verificationMethods={verificationMethods}
        verificationState={verificationState}
        onVerify={executeVerification}
        onCancel={cancelVerification}
        onCodeChange={setVerificationCode}
        onMethodSwitch={switchVerificationMethod}
        title={verificationState.title}
        description={verificationState.description}
      />
    </Card>
  );
};

export default TwoFactorAuth;
