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
import { Card, Typography, Button, Skeleton } from '@douyinfe/semi-ui';
import { IconCoinMoneyStroked } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

const WalletCard = ({ wallet, loading, CARD_PROPS }) => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  if (loading) {
    return (
      <Card {...CARD_PROPS} className='border-0 !rounded-2xl mb-4'>
        <Skeleton.Paragraph rows={2} />
      </Card>
    );
  }

  if (!wallet) return null;

  const isPlatform = wallet.source === 'platform';
  const balance = isPlatform ? wallet.balance : wallet.balance;
  const unit = isPlatform ? 'CNY' : '';

  return (
    <Card
      {...CARD_PROPS}
      className='border-0 !rounded-2xl mb-4 bg-gradient-to-r from-blue-50 to-indigo-50 dark:from-blue-900/20 dark:to-indigo-900/20'
    >
      <div className='flex items-center justify-between'>
        <div className='flex items-center gap-3'>
          <div className='w-10 h-10 rounded-xl bg-blue-100 dark:bg-blue-800/40 flex items-center justify-center'>
            <IconCoinMoneyStroked
              size='large'
              style={{ color: 'var(--semi-color-primary)' }}
            />
          </div>
          <div>
            <Typography.Text type='tertiary' size='small'>
              {isPlatform ? t('平台钱包余额') : t('账户余额')}
            </Typography.Text>
            <div className='flex items-baseline gap-2'>
              <Typography.Title heading={3} style={{ margin: 0 }}>
                {isPlatform
                  ? `¥${balance?.toFixed(2) || '0.00'}`
                  : balance?.toFixed(4) || '0.0000'}
              </Typography.Title>
              {isPlatform && wallet.frozen > 0 && (
                <Typography.Text type='warning' size='small'>
                  ({t('冻结')} ¥{wallet.frozen.toFixed(2)})
                </Typography.Text>
              )}
            </div>
          </div>
        </div>

        <div className='flex items-center gap-2'>
          {isPlatform && wallet.topup_url && (
            <Button
              theme='solid'
              type='primary'
              // Same reverse-tabnabbing exposure as the top-up page: this URL
              // comes from the platform and leaves the console tab reachable
              // through window.opener unless noopener is passed.
              onClick={() =>
                window.open(wallet.topup_url, '_blank', 'noopener,noreferrer')
              }
            >
              {t('充值')}
            </Button>
          )}
          <Button theme='light' onClick={() => navigate('/console/topup')}>
            {t('兑换码')}
          </Button>
        </div>
      </div>

      {isPlatform && (
        <div className='flex gap-6 mt-3 pt-3 border-t border-gray-200/50 dark:border-gray-700/50'>
          <div>
            <Typography.Text type='tertiary' size='small'>
              {t('累计充值')}
            </Typography.Text>
            <Typography.Text className='block'>
              ¥{wallet.lifetime_topup?.toFixed(2) || '0.00'}
            </Typography.Text>
          </div>
          <div>
            <Typography.Text type='tertiary' size='small'>
              {t('累计消费')}
            </Typography.Text>
            <Typography.Text className='block'>
              ¥{wallet.lifetime_spend?.toFixed(2) || '0.00'}
            </Typography.Text>
          </div>
        </div>
      )}
    </Card>
  );
};

export default WalletCard;
