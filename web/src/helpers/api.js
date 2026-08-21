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

import {
  getUserIdFromLocalStorage,
  showError,
  formatMessageForAPI,
  isValidMessage,
} from './utils';
import axios from 'axios';
import { MESSAGE_ROLES } from '../constants/playground.constants';
import { setTenantSlug } from './apiMode';

export let API = axios.create({
  baseURL: import.meta.env.VITE_REACT_APP_SERVER_URL
    ? import.meta.env.VITE_REACT_APP_SERVER_URL
    : '',
  headers: {
    'lurus-api-User': getUserIdFromLocalStorage(),
    'Cache-Control': 'no-store',
  },
});

function patchAPIInstance(instance) {
  const originalGet = instance.get.bind(instance);
  const inFlightGetRequests = new Map();

  const genKey = (url, config = {}) => {
    const params = config.params ? JSON.stringify(config.params) : '{}';
    return `${url}?${params}`;
  };

  instance.get = (url, config = {}) => {
    if (config?.disableDuplicate) {
      return originalGet(url, config);
    }

    const key = genKey(url, config);
    if (inFlightGetRequests.has(key)) {
      return inFlightGetRequests.get(key);
    }

    const reqPromise = originalGet(url, config).finally(() => {
      inFlightGetRequests.delete(key);
    });

    inFlightGetRequests.set(key, reqPromise);
    return reqPromise;
  };
}

patchAPIInstance(API);

// Layer C session self-heal. The v2 SPA keeps a *durable* localStorage `user`,
// but the credential the data APIs actually verify is the *session-scoped* gin
// session cookie established by POST /api/v2/auth/zita-bootstrap. When the two
// drift (the console is opened after the session expired) every data call 401s
// even though the UI still renders as "logged in". Re-establish the session once
// via the bridge, then replay the original request.
//
// `bootstrapInFlight` collapses the burst of parallel 401s a page mount triggers
// (the dashboard fires user/me + logs; the channels page fires a dozen) into a
// SINGLE bootstrap — every waiter shares it, then retries.
let bootstrapInFlight = null;

function ensureSession(instance) {
  if (!bootstrapInFlight) {
    bootstrapInFlight = instance
      .post('/api/v2/auth/zita-bootstrap', {}, { skipErrorHandler: true })
      .then((res) => {
        if (res?.data?.success && res.data.data?.id) {
          // Refresh the canonical user + slug so the shim stays in sync with the
          // freshly minted session (role/tenant may have changed server-side).
          localStorage.setItem('user', JSON.stringify(res.data.data));
          if (res.data.data.tenant_slug) {
            setTenantSlug(res.data.data.tenant_slug);
          }
          return res.data.data;
        }
        throw new Error('zita-bootstrap did not establish a session');
      })
      .finally(() => {
        bootstrapInFlight = null;
      });
  }
  return bootstrapInFlight;
}

function addResponseInterceptor(instance) {
  instance.interceptors.response.use(
    (response) => response,
    async (error) => {
      const config = error.config;
      // Skip global error handling if explicitly requested by the caller
      if (config && config.skipErrorHandler) {
        return Promise.reject(error);
      }
      // 401 self-heal: only when the SPA believes it is logged in (durable
      // `user` present), the request was not already retried, and the failing
      // call is not the bridge itself (avoid a heal→heal loop). Re-establish the
      // session, then replay the original request exactly once. The replay runs
      // with skipErrorHandler so a second failure surfaces here (single toast),
      // not twice.
      if (
        error.response?.status === 401 &&
        config &&
        !config._retriedAfterBootstrap &&
        !String(config.url || '').includes('zita-bootstrap') &&
        localStorage.getItem('user')
      ) {
        try {
          await ensureSession(instance);
          config._retriedAfterBootstrap = true;
          config.skipErrorHandler = true;
          return await instance(config);
        } catch (e) {
          // Bridge rejected (no platform session / account disabled) or the
          // replay still failed — fall through to the normal handler below,
          // which shows the toast or redirects to /login.
        }
      }
      showError(error);
      return Promise.reject(error);
    },
  );
}

addResponseInterceptor(API);

export function updateAPI() {
  API = axios.create({
    baseURL: import.meta.env.VITE_REACT_APP_SERVER_URL
      ? import.meta.env.VITE_REACT_APP_SERVER_URL
      : '',
    headers: {
      'lurus-api-User': getUserIdFromLocalStorage(),
      'Cache-Control': 'no-store',
    },
  });

  patchAPIInstance(API);
  addResponseInterceptor(API);
}

// playground

// 构建API请求负载
export const buildApiPayload = (
  messages,
  systemPrompt,
  inputs,
  parameterEnabled,
) => {
  const processedMessages = messages
    .filter(isValidMessage)
    .map(formatMessageForAPI)
    .filter(Boolean);

  // 如果有系统提示，插入到消息开头
  if (systemPrompt && systemPrompt.trim()) {
    processedMessages.unshift({
      role: MESSAGE_ROLES.SYSTEM,
      content: systemPrompt.trim(),
    });
  }

  const payload = {
    model: inputs.model,
    group: inputs.group,
    messages: processedMessages,
    stream: inputs.stream,
  };

  // 添加启用的参数
  const parameterMappings = {
    temperature: 'temperature',
    top_p: 'top_p',
    max_tokens: 'max_tokens',
    frequency_penalty: 'frequency_penalty',
    presence_penalty: 'presence_penalty',
    seed: 'seed',
  };

  Object.entries(parameterMappings).forEach(([key, param]) => {
    const enabled = parameterEnabled[key];
    const value = inputs[param];
    const hasValue = value !== undefined && value !== null;

    if (enabled && hasValue) {
      payload[param] = value;
    }
  });

  return payload;
};

// 处理API错误响应
export const handleApiError = (error, response = null) => {
  const message = error.message || '未知错误';
  const errorInfo = {
    error: message,
    timestamp: new Date().toISOString(),
    stack: error.stack,
  };

  if (response) {
    errorInfo.status = response.status;
    errorInfo.statusText = response.statusText;
  }

  if (message.includes('HTTP error')) {
    errorInfo.details = '服务器返回了错误状态码';
  } else if (message.includes('Failed to fetch')) {
    errorInfo.details = '网络连接失败或服务器无响应';
  }

  return errorInfo;
};

// 处理模型数据
export const processModelsData = (data, currentModel) => {
  const modelOptions = data.map((model) => ({
    label: model,
    value: model,
  }));

  const hasCurrentModel = modelOptions.some(
    (option) => option.value === currentModel,
  );
  const selectedModel =
    hasCurrentModel && modelOptions.length > 0
      ? currentModel
      : modelOptions[0]?.value;

  return { modelOptions, selectedModel };
};

// 处理分组数据
export const processGroupsData = (data, userGroup) => {
  let groupOptions = Object.entries(data).map(([group, info]) => ({
    label:
      info.desc.length > 20 ? info.desc.substring(0, 20) + '...' : info.desc,
    value: group,
    ratio: info.ratio,
    fullLabel: info.desc,
  }));

  if (groupOptions.length === 0) {
    groupOptions = [
      {
        label: '用户分组',
        value: '',
        ratio: 1,
      },
    ];
  } else if (userGroup) {
    const userGroupIndex = groupOptions.findIndex((g) => g.value === userGroup);
    if (userGroupIndex > -1) {
      const userGroupOption = groupOptions.splice(userGroupIndex, 1)[0];
      groupOptions.unshift(userGroupOption);
    }
  }

  return groupOptions;
};

let channelModels = undefined;
export async function loadChannelModels() {
  const res = await API.get('/api/models');
  const { success, data } = res.data;
  if (!success) {
    return;
  }
  channelModels = data;
  localStorage.setItem('channel_models', JSON.stringify(data));
}

export function getChannelModels(type) {
  if (channelModels !== undefined && type in channelModels) {
    if (!channelModels[type]) {
      return [];
    }
    return channelModels[type];
  }
  let models = localStorage.getItem('channel_models');
  if (!models) {
    return [];
  }
  channelModels = JSON.parse(models);
  if (type in channelModels) {
    return channelModels[type];
  }
  return [];
}
