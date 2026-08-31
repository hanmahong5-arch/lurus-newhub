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

import React, { useEffect, useState, useContext } from 'react';
import {
  API,
  showError,
  showInfo,
  showSuccess,
  renderQuota,
  isV2Mode,
  v2Url,
} from '../../helpers';
import { Modal } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { UserContext } from '../../context/User';
import { StatusContext } from '../../context/Status';

import RechargeCard from './RechargeCard';

const TopUp = () => {
  const { t } = useTranslation();
  const [userState, userDispatch] = useContext(UserContext);
  const [statusState] = useContext(StatusContext);

  const [redemptionCode, setRedemptionCode] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [topUpLink, setTopUpLink] = useState(
    statusState?.status?.top_up_link || '',
  );

  const topUp = async () => {
    if (redemptionCode === '') {
      showInfo(t('请输入兑换码！'));
      return;
    }
    setIsSubmitting(true);
    try {
      // v2 route is POST /api/v2/:slug/redeem (RedeemCodeV2). The previous
      // '/redemptions/redeem' path is registered nowhere and 404'd every v2
      // redemption from this page.
      const url = isV2Mode() ? v2Url('/redeem') : '/api/user/topup';
      const res = await API.post(url, { key: redemptionCode });
      const { success, message, data } = res.data;
      if (success) {
        // v1 returns the credited amount as a bare number; v2 wraps it as
        // { quota_added } (v2_redemption.go).
        const credited =
          typeof data === 'number' ? data : (data?.quota_added ?? 0);
        showSuccess(t('兑换成功！'));
        Modal.success({
          title: t('兑换成功！'),
          content: t('成功兑换额度：') + renderQuota(credited),
          centered: true,
        });
        if (userState.user) {
          const updatedUser = {
            ...userState.user,
            quota: userState.user.quota + credited,
          };
          userDispatch({ type: 'login', payload: updatedUser });
        }
        setRedemptionCode('');
      } else {
        showError(message);
      }
    } catch (err) {
      showError(t('请求失败'));
    } finally {
      setIsSubmitting(false);
    }
  };

  const openTopUpLink = () => {
    if (!topUpLink) {
      showError(t('超级管理员未设置充值链接！'));
      return;
    }
    // topUpLink is administrator-configured and points off-site. Without
    // noopener the payment page keeps a live window.opener handle back into
    // this authenticated console tab and can navigate it (reverse tabnabbing).
    window.open(topUpLink, '_blank', 'noopener,noreferrer');
  };

  const getUserQuota = async () => {
    const url = isV2Mode() ? v2Url('/user/me') : '/api/user/self';
    const res = await API.get(url);
    const { success, message, data } = res.data;
    if (success) {
      userDispatch({ type: 'login', payload: data });
    } else {
      showError(message);
    }
  };

  useEffect(() => {
    if (!userState?.user?.id) {
      getUserQuota();
    }
  }, []);

  useEffect(() => {
    if (statusState?.status) {
      setTopUpLink(statusState.status.top_up_link || '');
    }
  }, [statusState?.status]);

  return (
    <div className='w-full max-w-7xl mx-auto relative min-h-screen lg:min-h-0 mt-[60px] px-2'>
      <div className='space-y-6'>
        <div className='grid grid-cols-1 lg:grid-cols-12 gap-6'>
          <div className='lg:col-span-8 lg:col-start-3 space-y-6 w-full'>
            <RechargeCard
              t={t}
              redemptionCode={redemptionCode}
              setRedemptionCode={setRedemptionCode}
              topUp={topUp}
              isSubmitting={isSubmitting}
              topUpLink={topUpLink}
              openTopUpLink={openTopUpLink}
              userState={userState}
              renderQuota={renderQuota}
            />
          </div>
        </div>
      </div>
    </div>
  );
};

export default TopUp;
