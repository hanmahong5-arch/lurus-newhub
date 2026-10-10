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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

// Semi-UI is stubbed (its barrel needs canvas, absent in jsdom). The Form stub
// keeps the contract this page uses: field values, per-field `rules`
// validators run by formApi.validate().
const formState = { rules: {}, values: {} };
vi.mock('@douyinfe/semi-ui', () => {
  const Form = ({ children, getFormApi }) => {
    getFormApi({
      setValues: (v) => {
        formState.values = { ...v };
      },
      validate: () => {
        const bad = Object.entries(formState.rules).some(([f, rules]) =>
          rules.some((r) => !r.validator({}, formState.values[f])),
        );
        return bad ? Promise.reject(new Error('invalid')) : Promise.resolve();
      },
    });
    return React.createElement('form', null, children);
  };
  Form.TextArea = ({ field, rules, onChange }) => {
    formState.rules[field] = rules || [];
    return React.createElement('textarea', {
      'data-testid': `field-${field}`,
      onChange: (e) => {
        formState.values[field] = e.target.value;
        onChange && onChange(e.target.value);
      },
    });
  };
  Form.Switch = () => null;
  const Passthrough = ({ children }) =>
    React.createElement('div', null, children);
  return {
    Button: ({ children, onClick }) =>
      React.createElement('button', { onClick }, children),
    Col: Passthrough,
    Row: Passthrough,
    Space: Passthrough,
    Spin: Passthrough,
    Popconfirm: Passthrough,
    Form,
  };
});

vi.mock('../../../helpers', async () => ({
  API: { put: vi.fn(), post: vi.fn() },
  compareObjects: (a, b) =>
    Object.keys(a)
      .filter((k) => a[k] !== b[k])
      .map((key) => ({ key })),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  verifyJSON: (s) => {
    try {
      JSON.parse(s);
      return true;
    } catch (e) {
      return false;
    }
  },
}));

import ModelRatioSettings, {
  verifySearchUnitPrice,
} from './ModelRatioSettings';
import { API, showError } from '../../../helpers';

const options = {
  ModelPrice: '{}',
  SearchUnitPrice: '{"model-a": 0.002}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
};

describe('verifySearchUnitPrice', () => {
  it('accepts objects of non-negative numbers, explicit 0 and blank', () => {
    expect(verifySearchUnitPrice('{"model-a": 0.002}')).toBe(true);
    expect(verifySearchUnitPrice('{"model-a": 0}')).toBe(true);
    expect(verifySearchUnitPrice('{}')).toBe(true);
    expect(verifySearchUnitPrice('')).toBe(true);
    expect(verifySearchUnitPrice(undefined)).toBe(true);
  });

  it('rejects non-JSON, non-objects, negative and non-numeric values', () => {
    expect(verifySearchUnitPrice('{bad')).toBe(false);
    expect(verifySearchUnitPrice('[1]')).toBe(false);
    expect(verifySearchUnitPrice('0.5')).toBe(false);
    expect(verifySearchUnitPrice('null')).toBe(false);
    expect(verifySearchUnitPrice('{"model-a": -1}')).toBe(false);
    expect(verifySearchUnitPrice('{"model-a": "0.1"}')).toBe(false);
  });
});

describe('ModelRatioSettings SearchUnitPrice field', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    formState.rules = {};
    formState.values = {};
  });

  it('renders the box and refuses to save an invalid value', async () => {
    render(<ModelRatioSettings options={options} refresh={vi.fn()} />);
    const box = screen.getByTestId('field-SearchUnitPrice');
    fireEvent.change(box, { target: { value: '{"model-a": -3}' } });
    fireEvent.click(screen.getByText('保存模型倍率设置'));
    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(API.put).not.toHaveBeenCalled();
  });

  it('submits a valid value through the options endpoint', async () => {
    API.put.mockResolvedValue({ data: { success: true } });
    render(<ModelRatioSettings options={options} refresh={vi.fn()} />);
    fireEvent.change(screen.getByTestId('field-SearchUnitPrice'), {
      target: { value: '{"model-a": 0}' },
    });
    fireEvent.click(screen.getByText('保存模型倍率设置'));
    await waitFor(() =>
      expect(API.put).toHaveBeenCalledWith('/api/option/', {
        key: 'SearchUnitPrice',
        value: '{"model-a": 0}',
      }),
    );
  });
});
