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

import React, { useEffect, useState, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  API,
  showError,
  showSuccess,
  renderQuota,
  renderQuotaWithPrompt,
} from '../../../../helpers';
import { useIsMobile } from '../../../../hooks/common/useIsMobile';
import {
  Button,
  Modal,
  SideSheet,
  Space,
  Spin,
  Typography,
  Card,
  Tag,
  Form,
  Avatar,
  Row,
  Col,
  Input,
  InputNumber,
  Progress,
  Descriptions,
} from '@douyinfe/semi-ui';
import {
  IconUser,
  IconSave,
  IconClose,
  IconLink,
  IconUserGroup,
  IconPlus,
  IconClock,
} from '@douyinfe/semi-icons';

const { Text, Title } = Typography;

const EditUserModal = (props) => {
  const { t } = useTranslation();
  const userId = props.editingUser.id;
  const [loading, setLoading] = useState(true);
  const [addQuotaModalOpen, setIsModalOpen] = useState(false);
  const [addQuotaLocal, setAddQuotaLocal] = useState('');
  const isMobile = useIsMobile();
  const [groupOptions, setGroupOptions] = useState([]);
  const formApiRef = useRef(null);

  const isEdit = Boolean(userId);

  const getInitValues = () => ({
    username: '',
    display_name: '',
    password: '',
    github_id: '',
    oidc_id: '',
    discord_id: '',
    wechat_id: '',
    telegram_id: '',
    email: '',
    quota: 0,
    group: 'default',
    remark: '',
    daily_quota: 0,
    base_group: '',
    fallback_group: '',
  });

  const fetchGroups = async () => {
    try {
      let res = await API.get(`/api/group/`);
      setGroupOptions(res.data.data.map((g) => ({ label: g, value: g })));
    } catch (e) {
      showError(e.message);
    }
  };

  const handleCancel = () => props.handleClose();

  const loadUser = async () => {
    setLoading(true);
    const url = userId ? `/api/user/${userId}` : `/api/user/self`;
    const res = await API.get(url);
    const { success, message, data } = res.data;
    if (success) {
      data.password = '';
      formApiRef.current?.setValues({ ...getInitValues(), ...data });
    } else {
      showError(message);
    }
    setLoading(false);
  };

  useEffect(() => {
    loadUser();
    if (userId) {
      fetchGroups();
    }
  }, [props.editingUser.id]);

  /* ----------------------- submit ----------------------- */
  const submit = async (values) => {
    setLoading(true);
    try {
      let payload = { ...values };
      if (typeof payload.quota === 'string')
        payload.quota = parseInt(payload.quota) || 0;
      if (typeof payload.daily_quota === 'string')
        payload.daily_quota = parseInt(payload.daily_quota) || 0;
      if (userId) {
        payload.id = parseInt(userId);
      }
      const url = userId ? `/api/user/` : `/api/user/self`;
      const res = await API.put(url, payload);
      const { success, message } = res.data;
      if (success) {
        showSuccess(t('用户信息更新成功！'));
        props.refresh();
        props.handleClose();
      } else {
        showError(message);
      }
    } catch (e) {
      // 请求本身失败（离线、502、会话掉线）时不能让异常逃出 handler：那样既不会
      // 报错也不会解掉 loading，抽屉会永远转圈，管理员分不清"还在存"和"没存上"。
      showError(e.message);
    } finally {
      setLoading(false);
    }
  };

  /* --------------------- quota helper -------------------- */
  // 预览和实际写入必须用同一个解析结果：一边 parseInt(x || 0)、另一边
  // parseInt(x) || 0 时，非数字输入会让预览显示 $NaN，而确定实际只加 0。
  const addQuotaDelta = parseInt(addQuotaLocal) || 0;

  // 余额同理：预览直接用表单里的原值，而写入先 parseInt。表单里是字符串时
  // '1500000' + 500000 会拼成 1500000500000，预览报出天文数字而确定只加 $1。
  const currentQuota = () =>
    parseInt(formApiRef.current?.getValue('quota')) || 0;

  const addLocalQuota = () => {
    formApiRef.current?.setValue('quota', currentQuota() + addQuotaDelta);
  };

  // 关闭时清空增量：留在 state 里的话，下次打开对话框会带着上一次的金额，
  // 顺手点确定就把刚发出去的额度又发了一遍。
  const closeAddQuotaModal = () => {
    setAddQuotaLocal('');
    setIsModalOpen(false);
  };

  /* --------------------------- UI --------------------------- */
  return (
    <>
      <SideSheet
        placement='right'
        title={
          <Space>
            <Tag color='blue' shape='circle'>
              {t(isEdit ? '编辑' : '新建')}
            </Tag>
            <Title heading={4} className='m-0'>
              {isEdit ? t('编辑用户') : t('创建用户')}
            </Title>
          </Space>
        }
        bodyStyle={{ padding: 0 }}
        visible={props.visible}
        width={isMobile ? '100%' : 600}
        footer={
          <div className='flex justify-end bg-white'>
            <Space>
              <Button
                theme='solid'
                onClick={() => formApiRef.current?.submitForm()}
                icon={<IconSave />}
                loading={loading}
              >
                {t('提交')}
              </Button>
              <Button
                theme='light'
                type='primary'
                onClick={handleCancel}
                icon={<IconClose />}
              >
                {t('取消')}
              </Button>
            </Space>
          </div>
        }
        closeIcon={null}
        onCancel={handleCancel}
      >
        <Spin spinning={loading}>
          <Form
            initValues={getInitValues()}
            getFormApi={(api) => (formApiRef.current = api)}
            onSubmit={submit}
          >
            {({ values }) => (
              <div className='p-2'>
                {/* 基本信息 */}
                <Card className='!rounded-2xl shadow-sm border-0'>
                  <div className='flex items-center mb-2'>
                    <Avatar
                      size='small'
                      color='blue'
                      className='mr-2 shadow-md'
                    >
                      <IconUser size={16} />
                    </Avatar>
                    <div>
                      <Text className='text-lg font-medium'>
                        {t('基本信息')}
                      </Text>
                      <div className='text-xs text-gray-600'>
                        {t('用户的基本账户信息')}
                      </div>
                    </div>
                  </div>

                  <Row gutter={12}>
                    <Col span={24}>
                      <Form.Input
                        field='username'
                        label={t('用户名')}
                        placeholder={t('请输入新的用户名')}
                        rules={[{ required: true, message: t('请输入用户名') }]}
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='password'
                        label={t('密码')}
                        placeholder={t('请输入新的密码，最短 8 位')}
                        mode='password'
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='display_name'
                        label={t('显示名称')}
                        placeholder={t('请输入新的显示名称')}
                        showClear
                      />
                    </Col>

                    <Col span={24}>
                      <Form.Input
                        field='remark'
                        label={t('备注')}
                        placeholder={t('请输入备注（仅管理员可见）')}
                        showClear
                      />
                    </Col>
                  </Row>
                </Card>

                {/* 权限设置 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center mb-2'>
                      <Avatar
                        size='small'
                        color='green'
                        className='mr-2 shadow-md'
                      >
                        <IconUserGroup size={16} />
                      </Avatar>
                      <div>
                        <Text className='text-lg font-medium'>
                          {t('权限设置')}
                        </Text>
                        <div className='text-xs text-gray-600'>
                          {t('用户分组和额度管理')}
                        </div>
                      </div>
                    </div>

                    <Row gutter={12}>
                      <Col span={24}>
                        <Form.Select
                          field='group'
                          label={t('分组')}
                          placeholder={t('请选择分组')}
                          optionList={groupOptions}
                          allowAdditions
                          search
                          rules={[{ required: true, message: t('请选择分组') }]}
                        />
                      </Col>

                      <Col span={10}>
                        <Form.InputNumber
                          field='quota'
                          label={t('剩余额度')}
                          placeholder={t('请输入新的剩余额度')}
                          step={500000}
                          extraText={renderQuotaWithPrompt(values.quota || 0)}
                          rules={[{ required: true, message: t('请输入额度') }]}
                          style={{ width: '100%' }}
                        />
                      </Col>

                      <Col span={14}>
                        <Form.Slot label={t('添加额度')}>
                          <Button
                            icon={<IconPlus />}
                            onClick={() => setIsModalOpen(true)}
                          />
                        </Form.Slot>
                      </Col>
                    </Row>
                  </Card>
                )}

                {/* 每日额度配置 */}
                {userId && (
                  <Card className='!rounded-2xl shadow-sm border-0'>
                    <div className='flex items-center mb-2'>
                      <Avatar
                        size='small'
                        color='orange'
                        className='mr-2 shadow-md'
                      >
                        <IconClock size={16} />
                      </Avatar>
                      <div>
                        <Text className='text-lg font-medium'>
                          {t('每日额度配置')}
                        </Text>
                        <div className='text-xs text-gray-600'>
                          {t('每日使用限额和降级分组设置')}
                        </div>
                      </div>
                    </div>

                    <Row gutter={12}>
                      <Col span={24}>
                        <Form.InputNumber
                          field='daily_quota'
                          label={t('每日额度限制')}
                          placeholder={t('0 表示无限制')}
                          step={100000}
                          min={0}
                          extraText={
                            values.daily_quota > 0
                              ? renderQuotaWithPrompt(values.daily_quota)
                              : t('当前设置：无限制')
                          }
                          style={{ width: '100%' }}
                        />
                      </Col>

                      <Col span={12}>
                        <Form.Select
                          field='base_group'
                          label={t('基础分组')}
                          placeholder={t('订阅原始分组')}
                          optionList={groupOptions}
                          allowAdditions
                          search
                          showClear
                        />
                      </Col>

                      <Col span={12}>
                        <Form.Select
                          field='fallback_group'
                          label={t('降级分组')}
                          placeholder={t('额度耗尽后使用的分组')}
                          optionList={groupOptions}
                          allowAdditions
                          search
                          showClear
                        />
                      </Col>
                    </Row>
                  </Card>
                )}

                {/* 绑定信息 */}
                <Card className='!rounded-2xl shadow-sm border-0'>
                  <div className='flex items-center mb-2'>
                    <Avatar
                      size='small'
                      color='purple'
                      className='mr-2 shadow-md'
                    >
                      <IconLink size={16} />
                    </Avatar>
                    <div>
                      <Text className='text-lg font-medium'>
                        {t('绑定信息')}
                      </Text>
                      <div className='text-xs text-gray-600'>
                        {t('第三方账户绑定状态（只读）')}
                      </div>
                    </div>
                  </div>

                  <Row gutter={12}>
                    {[
                      'github_id',
                      'discord_id',
                      'oidc_id',
                      'wechat_id',
                      'email',
                      'telegram_id',
                    ].map((field) => (
                      <Col span={24} key={field}>
                        <Form.Input
                          field={field}
                          label={t('已绑定的 {{provider}} 账户', {
                            provider: field.replace('_id', '').toUpperCase(),
                          })}
                          readonly
                          placeholder={t(
                            '此项只读，需要用户通过个人设置页面的相关绑定按钮进行绑定，不可直接修改',
                          )}
                        />
                      </Col>
                    ))}
                  </Row>
                </Card>
              </div>
            )}
          </Form>
        </Spin>
      </SideSheet>

      {/* 添加额度模态框 */}
      <Modal
        centered
        visible={addQuotaModalOpen}
        onOk={() => {
          addLocalQuota();
          closeAddQuotaModal();
        }}
        onCancel={closeAddQuotaModal}
        closable={null}
        title={
          <div className='flex items-center'>
            <IconPlus className='mr-2' />
            {t('添加额度')}
          </div>
        }
      >
        <div className='mb-4'>
          {(() => {
            const current = currentQuota();
            return (
              <Text type='secondary' className='block mb-2'>
                {`${t('新额度：')}${renderQuota(current)} + ${renderQuota(addQuotaDelta)} = ${renderQuota(current + addQuotaDelta)}`}
              </Text>
            );
          })()}
        </div>
        <InputNumber
          placeholder={t('需要添加的额度（支持负数）')}
          value={addQuotaLocal}
          onChange={setAddQuotaLocal}
          style={{ width: '100%' }}
          showClear
          step={500000}
        />
      </Modal>
    </>
  );
};

export default EditUserModal;
