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

// The settings strip above the channel table: three view toggles and a
// "maintenance" dropdown holding one irreversible action (delete every
// disabled channel) and one that rewrites the ability table.
//
// What is worth asserting: (a) the maintenance actions are all behind a
// confirm and none of them fires on click alone; (b) each toggle persists its
// choice AND reloads with the value the user just picked, not the stale state
// value — an off-by-one-render here silently shows the operator the previous
// filter's rows.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({ confirm: vi.fn() }));

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ children, onClick, disabled, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, disabled, ...rest },
      children,
    );
  // The dropdown's contents are the whole point of the component, so render
  // them inline instead of hiding them behind a trigger that never opens.
  const Dropdown = ({ render: menu, children }) =>
    React.createElement('div', { 'data-testid': 'dropdown' }, children, menu);
  Dropdown.Menu = ({ children }) =>
    React.createElement('div', { 'data-testid': 'dropdown-menu' }, children);
  Dropdown.Item = ({ children }) =>
    React.createElement('div', { 'data-testid': 'dropdown-item' }, children);

  const Switch = ({ checked, onChange, ...rest }) =>
    React.createElement('input', {
      type: 'checkbox',
      role: 'switch',
      checked: !!checked,
      onChange: (e) => onChange(e.target.checked),
      ...rest,
    });

  const Select = ({ value, onChange, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'status-select', 'data-value': String(value) },
      React.Children.map(children, (child) =>
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `status-opt-${child.props.value}`,
            onClick: () => onChange(child.props.value),
          },
          child.props.children,
        ),
      ),
    );
  Select.Option = ({ children }) => React.createElement('span', null, children);

  return {
    Button,
    Dropdown,
    Switch,
    Select,
    Modal: { confirm: H.confirm },
    Typography: {
      Text: ({ children, ...rest }) =>
        React.createElement('span', rest, children),
    },
  };
});

import ChannelsActions from './ChannelsActions';

const t = (k) => k;

const makeProps = (overrides = {}) => ({
  t,
  setShowBatchSetTag: vi.fn(),
  enableBatchDelete: true,
  fixChannelsAbilities: vi.fn(),
  updateAllChannelsBalance: vi.fn(),
  deleteAllDisabledChannels: vi.fn(),
  idSort: false,
  setIdSort: vi.fn(),
  enableTagMode: false,
  setEnableTagMode: vi.fn(),
  statusFilter: 'all',
  setStatusFilter: vi.fn(),
  getFormValues: vi.fn(() => ({
    searchKeyword: '',
    searchGroup: '',
    searchModel: '',
  })),
  loadChannels: vi.fn(),
  searchChannels: vi.fn(),
  activeTypeKey: 'all',
  activePage: 3,
  pageSize: 20,
  setActivePage: vi.fn(),
  ...overrides,
});

const renderActions = (overrides = {}) => {
  const props = makeProps(overrides);
  render(<ChannelsActions {...props} />);
  return props;
};

const switchByLabel = (label) => {
  const row = screen.getByText(label).parentElement;
  return row.querySelector('[role="switch"]');
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

describe('maintenance dropdown', () => {
  it.each([
    [
      '修复数据库一致性',
      'fixChannelsAbilities',
      '确定是否要修复数据库一致性？',
    ],
    ['更新所有已启用通道余额', 'updateAllChannelsBalance', '确定？'],
    ['删除禁用通道', 'deleteAllDisabledChannels', '确定是否要删除禁用通道？'],
  ])(
    '%s does not run until the confirm dialog is accepted',
    async (label, handlerName, title) => {
      const user = userEvent.setup();
      const props = renderActions();

      await user.click(screen.getByText(label));
      expect(props[handlerName]).not.toHaveBeenCalled();
      expect(H.confirm).toHaveBeenCalledTimes(1);
      expect(H.confirm.mock.calls[0][0].title).toBe(title);

      H.confirm.mock.calls[0][0].onOk();
      expect(props[handlerName]).toHaveBeenCalledTimes(1);
    },
  );

  it('tells the operator the disabled-channel purge is irreversible', async () => {
    const user = userEvent.setup();
    renderActions();
    await user.click(screen.getByText('删除禁用通道'));
    expect(H.confirm.mock.calls[0][0].content).toBe('此修改将不可逆');
  });
});

describe('batch tag button', () => {
  it('is available only while row selection is on', () => {
    renderActions({ enableBatchDelete: true });
    expect(screen.getByText('批量设置标签').closest('button')).toBeEnabled();
  });

  it('is refused when row selection is off', async () => {
    const user = userEvent.setup();
    const props = renderActions({ enableBatchDelete: false });
    const btn = screen.getByText('批量设置标签').closest('button');
    expect(btn).toBeDisabled();
    await user.click(btn);
    expect(props.setShowBatchSetTag).not.toHaveBeenCalled();
  });
});

describe('ID sort toggle', () => {
  it('persists the choice and reloads with the NEW value, not the stale one', async () => {
    const user = userEvent.setup();
    const props = renderActions({ idSort: false });

    await user.click(switchByLabel('使用ID排序'));

    expect(localStorage.getItem('id-sort')).toBe('true');
    expect(props.setIdSort).toHaveBeenCalledWith(true);
    // Third positional arg is idSort: it must be the value just chosen.
    expect(props.loadChannels).toHaveBeenCalledWith(3, 20, true, false);
    expect(props.searchChannels).not.toHaveBeenCalled();
  });

  it('re-runs the SEARCH instead of the plain list when a filter is active', async () => {
    const user = userEvent.setup();
    const props = renderActions({
      getFormValues: () => ({
        searchKeyword: 'openai',
        searchGroup: '',
        searchModel: '',
      }),
      statusFilter: 'enabled',
      activeTypeKey: '1',
    });

    await user.click(switchByLabel('使用ID排序'));

    // Reloading the unfiltered list here would drop the operator's query.
    expect(props.loadChannels).not.toHaveBeenCalled();
    expect(props.searchChannels).toHaveBeenCalledWith(
      false,
      '1',
      'enabled',
      3,
      20,
      true,
    );
  });
});

describe('tag aggregation toggle', () => {
  it('persists, resets to page 1 and reloads with the new mode', async () => {
    const user = userEvent.setup();
    const props = renderActions({ enableTagMode: false, idSort: true });

    await user.click(switchByLabel('标签聚合模式'));

    expect(localStorage.getItem('enable-tag-mode')).toBe('true');
    expect(props.setEnableTagMode).toHaveBeenCalledWith(true);
    // Staying on page 3 of the ungrouped list after grouping would show an
    // empty table.
    expect(props.setActivePage).toHaveBeenCalledWith(1);
    expect(props.loadChannels).toHaveBeenCalledWith(1, 20, true, true);
  });

  it('turns tag mode back off again', async () => {
    const user = userEvent.setup();
    const props = renderActions({ enableTagMode: true });
    await user.click(switchByLabel('标签聚合模式'));
    expect(localStorage.getItem('enable-tag-mode')).toBe('false');
    expect(props.loadChannels).toHaveBeenCalledWith(1, 20, false, false);
  });
});

describe('status filter', () => {
  it('persists the selection and reloads page 1 carrying type and status', async () => {
    const user = userEvent.setup();
    const props = renderActions({ activeTypeKey: '14', idSort: true });

    await user.click(screen.getByTestId('status-opt-disabled'));

    expect(localStorage.getItem('channel-status-filter')).toBe('disabled');
    expect(props.setStatusFilter).toHaveBeenCalledWith('disabled');
    expect(props.setActivePage).toHaveBeenCalledWith(1);
    expect(props.loadChannels).toHaveBeenCalledWith(
      1,
      20,
      true,
      false,
      '14',
      'disabled',
    );
  });

  it('offers exactly the three documented states', () => {
    renderActions();
    expect(screen.getByTestId('status-opt-all')).toBeInTheDocument();
    expect(screen.getByTestId('status-opt-enabled')).toBeInTheDocument();
    expect(screen.getByTestId('status-opt-disabled')).toBeInTheDocument();
  });

  it('reflects the active filter back to the control', () => {
    renderActions({ statusFilter: 'enabled' });
    expect(screen.getByTestId('status-select').dataset.value).toBe('enabled');
  });
});
