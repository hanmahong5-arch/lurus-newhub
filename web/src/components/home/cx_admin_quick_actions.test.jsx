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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

const navigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));

vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Card: ({ children }) =>
      React.createElement('section', { 'data-testid': 'card' }, children),
    Button: ({ children, onClick, icon }) =>
      React.createElement(
        'button',
        { type: 'button', onClick },
        icon,
        children,
      ),
    Typography,
    Tag: ({ children, color }) =>
      React.createElement(
        'span',
        { 'data-testid': 'stat-tag', 'data-color': color },
        children,
      ),
    Space: ({ children }) =>
      React.createElement('div', { 'data-testid': 'stats' }, children),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconPlus: () => React.createElement('i', { 'data-testid': 'icon-plus' }),
  IconKey: () => React.createElement('i', { 'data-testid': 'icon-key' }),
  IconList: () => React.createElement('i', { 'data-testid': 'icon-list' }),
  IconSetting: () =>
    React.createElement('i', { 'data-testid': 'icon-setting' }),
}));

const apiGet = vi.fn();
vi.mock('../../helpers', () => ({
  API: { get: (...a) => apiGet(...a) },
}));

import AdminQuickActions from './AdminQuickActions';

const totals = ({ channels = 0, tokens = 0, models = 0 } = {}) => {
  apiGet.mockImplementation((url) => {
    if (url.startsWith('/api/channel'))
      return Promise.resolve({ data: { total: channels } });
    if (url.startsWith('/api/token'))
      return Promise.resolve({ data: { total: tokens } });
    return Promise.resolve({ data: { total: models } });
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  totals({});
});

describe('AdminQuickActions', () => {
  it.each([
    ['admin_action_add_channel', '/console/channel'],
    ['admin_action_create_token', '/console/token'],
    ['admin_action_view_logs', '/console/log'],
    ['admin_action_settings', '/console/setting'],
  ])('sends %s to %s', async (label, path) => {
    render(React.createElement(AdminQuickActions, null));
    fireEvent.click(screen.getByText(label));
    expect(navigate).toHaveBeenCalledWith(path);
    expect(navigate).toHaveBeenCalledTimes(1);
  });

  it('queries all three inventories once on mount', async () => {
    render(React.createElement(AdminQuickActions, null));
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(3));
    expect(apiGet).toHaveBeenCalledWith('/api/channel/?p=0&size=1');
    expect(apiGet).toHaveBeenCalledWith('/api/token/?p=0&size=1');
    expect(apiGet).toHaveBeenCalledWith('/api/models/?p=0&size=1');
  });

  it('labels each total with the inventory it belongs to', async () => {
    totals({ channels: 3, tokens: 11, models: 7 });
    render(React.createElement(AdminQuickActions, null));
    await screen.findByTestId('stats');
    const tags = screen.getAllByTestId('stat-tag');
    // Order matters: channels / models / tokens, each with its own colour.
    expect(tags[0].textContent.replace(/\s+/g, ' ').trim()).toBe(
      '3 admin_stat_channels',
    );
    expect(tags[1].textContent.replace(/\s+/g, ' ').trim()).toBe(
      '7 admin_stat_models',
    );
    expect(tags[2].textContent.replace(/\s+/g, ' ').trim()).toBe(
      '11 admin_stat_tokens',
    );
    expect(tags.map((x) => x.getAttribute('data-color'))).toEqual([
      'green',
      'blue',
      'cyan',
    ]);
  });

  it('shows a real zero rather than a blank when an inventory is empty', async () => {
    totals({ channels: 0, tokens: 0, models: 0 });
    render(React.createElement(AdminQuickActions, null));
    await screen.findByTestId('stats');
    expect(
      screen.getAllByTestId('stat-tag').every((x) => /\d/.test(x.textContent)),
    ).toBe(true);
  });

  it('falls back to zero when the response omits a total', async () => {
    apiGet.mockResolvedValue({ data: {} });
    render(React.createElement(AdminQuickActions, null));
    await screen.findByTestId('stats');
    const tags = screen.getAllByTestId('stat-tag');
    expect(tags[0].textContent.replace(/\s+/g, ' ').trim()).toBe(
      '0 admin_stat_channels',
    );
  });

  it('keeps the action buttons usable when the inventory probe fails', async () => {
    apiGet.mockRejectedValue(new Error('offline'));
    render(React.createElement(AdminQuickActions, null));
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(3));
    // No counters — but the quick actions themselves must survive.
    expect(screen.queryByTestId('stats')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('admin_action_add_channel'));
    expect(navigate).toHaveBeenCalledWith('/console/channel');
  });
});
