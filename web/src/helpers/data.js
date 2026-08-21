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

// localStorage 会把入参字符串化，缺失字段会被写成字面量 "undefined"——
// 一个真值字符串，足以让下游所有 `if (!value)` 兜底失效。缺失就删除键。
function setOrRemove(key, value) {
  if (value === undefined || value === null) {
    localStorage.removeItem(key);
    return;
  }
  localStorage.setItem(key, value);
}

export function setStatusData(data) {
  localStorage.setItem('status', JSON.stringify(data));
  setOrRemove('system_name', data.system_name);
  setOrRemove('logo', data.logo);
  setOrRemove('footer_html', data.footer_html);
  setOrRemove('quota_per_unit', data.quota_per_unit);
  // 兼容：保留旧字段，同时写入新的额度展示类型
  setOrRemove('display_in_currency', data.display_in_currency);
  localStorage.setItem('quota_display_type', data.quota_display_type || 'USD');
  setOrRemove('enable_drawing', data.enable_drawing);
  setOrRemove('enable_task', data.enable_task);
  setOrRemove('enable_data_export', data.enable_data_export);
  setOrRemove('chats', JSON.stringify(data.chats));
  setOrRemove('data_export_default_time', data.data_export_default_time);
  setOrRemove('default_collapse_sidebar', data.default_collapse_sidebar);
  setOrRemove('mj_notify_enabled', data.mj_notify_enabled);
  if (data.chat_link) {
    // localStorage.setItem('chat_link', data.chat_link);
  } else {
    localStorage.removeItem('chat_link');
  }
  if (data.chat_link2) {
    // localStorage.setItem('chat_link2', data.chat_link2);
  } else {
    localStorage.removeItem('chat_link2');
  }
  if (data.docs_link) {
    localStorage.setItem('docs_link', data.docs_link);
  } else {
    localStorage.removeItem('docs_link');
  }
}

export function setUserData(data) {
  localStorage.setItem('user', JSON.stringify(data));
}
