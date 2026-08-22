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
import { useCallback, useEffect, useState } from 'react';
import { API, showError, toBoolean } from '../../helpers';
import i18next from '../../i18n/i18n';

/**
 * Shared hook for fetching and managing /api/option/ settings.
 *
 * @param {Object} initialState - Default field values. Boolean fields are auto-detected for parsing.
 * @param {Object} [opts]
 * @param {Function} [opts.parseItem] - Custom parser: (key, value, item) => parsedValue | undefined.
 *   Return `undefined` to fall back to default parsing.
 * @returns {{ inputs, setInputs, loading, refresh, updateOption }}
 */
export default function useSettingsOptions(initialState, opts = {}) {
  const { parseItem } = opts;
  const [inputs, setInputs] = useState(initialState);
  const [loading, setLoading] = useState(false);

  const getOptions = useCallback(async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      const newInputs = {};
      data.forEach((item) => {
        // Allow custom parsing first
        if (parseItem) {
          const custom = parseItem(item.key, item.value, item);
          if (custom !== undefined) {
            newInputs[item.key] = custom;
            return;
          }
        }
        // Default: auto-detect boolean fields from initialState
        if (typeof initialState[item.key] === 'boolean') {
          newInputs[item.key] = toBoolean(item.value);
        } else if (item.key in initialState) {
          newInputs[item.key] = item.value;
        }
      });
      setInputs(newInputs);
      return newInputs;
    } else {
      showError(message);
      return null;
    }
  }, []);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      return await getOptions();
    } catch (error) {
      showError(i18next.t('刷新失败'));
      return null;
    } finally {
      setLoading(false);
    }
  }, [getOptions]);

  const updateOption = useCallback(async (key, value) => {
    setLoading(true);
    try {
      const res = await API.put('/api/option/', { key, value });
      const { success, message } = res.data;
      if (success) {
        setInputs((prev) => ({ ...prev, [key]: value }));
        return true;
      } else {
        showError(message);
        return false;
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, []);

  return { inputs, setInputs, loading, setLoading, refresh, updateOption };
}
