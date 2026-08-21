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
import { Modal } from '@douyinfe/semi-ui';

const EnableDisableUserModal = ({
  visible,
  onCancel,
  onConfirm,
  user,
  action,
  t,
}) => {
  // 只有明确认识的动作才配上对应文案：任何其它取值（拼写错误、改名后的常量、
  // 首次打开前表格里的空串）都不能被说成"启用"，否则一个即将封停账户的对话框
  // 会反过来告诉管理员它要把账户打开。
  const isDisable = action === 'disable';
  const isEnable = action === 'enable';

  let title = t('确认操作');
  let description = t('此操作具有风险，请确认要继续执行');
  if (isDisable) {
    title = t('确定要禁用此用户吗？');
    description = t('此操作将禁用用户账户');
  } else if (isEnable) {
    title = t('确定要启用此用户吗？');
    description = t('此操作将启用用户账户');
  }

  return (
    <Modal
      title={title}
      visible={visible}
      onCancel={onCancel}
      onOk={onConfirm}
      type='warning'
    >
      {description}
      {user?.username ? `：${user.username}` : ''}
    </Modal>
  );
};

export default EnableDisableUserModal;
