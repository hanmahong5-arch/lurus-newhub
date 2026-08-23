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
import { Button } from '@douyinfe/semi-ui';
import CompactModeToggle from '../../common/ui/CompactModeToggle';

// The batch-delete controls that used to live here (row-selection checkboxes,
// a Popconfirm-gated "批量删除" button) posted to POST
// /api/deployments/batch_delete, a route nothing in the tree registers, and
// the page above had already hard-disabled the feature (batchOperationsEnabled
// = false) so none of it ever rendered. Removed rather than left as dead,
// permanently-off UI.
const DeploymentsActions = ({
  setEditingDeployment,
  setShowEdit,
  compactMode,
  setCompactMode,
  showCreateModal,
  setShowCreateModal,
  t,
}) => {
  const handleAddDeployment = () => {
    if (setShowCreateModal) {
      setShowCreateModal(true);
    } else {
      // Fallback to old behavior if setShowCreateModal is not provided
      setEditingDeployment({ id: undefined });
      setShowEdit(true);
    }
  };

  return (
    <div className='flex flex-wrap gap-2 w-full md:w-auto order-2 md:order-1'>
      <Button
        type='primary'
        className='flex-1 md:flex-initial'
        onClick={handleAddDeployment}
        size='small'
      >
        {t('新建容器')}
      </Button>

      {/* Compact Mode */}
      <CompactModeToggle
        compactMode={compactMode}
        setCompactMode={setCompactMode}
        t={t}
      />
    </div>
  );
};

export default DeploymentsActions;
