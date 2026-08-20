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

// Search / add / reset strip. Small, but it owns the "add channel" entry
// point — which must open a BLANK editor, never the last-edited channel —
// and the reset button, which must actually clear the form rather than just
// re-running the previous query.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ children, onClick, htmlType, loading, ...rest }) =>
    React.createElement(
      'button',
      {
        type: htmlType === 'submit' ? 'submit' : 'button',
        onClick,
        'data-loading': loading ? 'true' : 'false',
        ...rest,
      },
      children,
    );

  const Form = ({ children, onSubmit, initValues, getFormApi }) => {
    // Hand the parent a form api exactly as Semi does, so the reset path is
    // reachable.
    React.useEffect(() => {
      getFormApi?.({ reset: () => {}, getValues: () => initValues });
    }, []);
    return React.createElement(
      'form',
      {
        'data-testid': 'form',
        'data-init': JSON.stringify(initValues ?? null),
        onSubmit: (e) => {
          e.preventDefault();
          onSubmit();
        },
      },
      children,
    );
  };
  Form.Input = ({ field, placeholder, prefix }) =>
    React.createElement(
      'span',
      null,
      prefix,
      React.createElement('input', {
        'data-testid': `field-${field}`,
        placeholder,
        readOnly: true,
      }),
    );
  Form.Select = ({ field, placeholder, optionList, onChange }) =>
    React.createElement(
      'div',
      {
        'data-testid': `field-${field}`,
        'data-options': JSON.stringify(optionList),
      },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': `pick-${field}`,
          onClick: () => onChange('vip'),
        },
        placeholder,
      ),
    );

  return { Button, Form };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconSearch: () => React.createElement('i', { 'data-testid': 'icon-search' }),
}));

import ChannelsFilters from './ChannelsFilters';

const t = (k) => k;

const makeProps = (over = {}) => ({
  t,
  setEditingChannel: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn(),
  setShowColumnSelector: vi.fn(),
  formInitValues: { searchKeyword: '', searchGroup: '', searchModel: '' },
  setFormApi: vi.fn(),
  searchChannels: vi.fn(),
  enableTagMode: false,
  formApi: { reset: vi.fn() },
  groupOptions: [{ label: 'vip', value: 'vip' }],
  loading: false,
  searching: false,
  ...over,
});

const renderFilters = (over = {}) => {
  const props = makeProps(over);
  render(<ChannelsFilters {...props} />);
  return props;
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers({ shouldAdvanceTime: true });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('add channel', () => {
  it('opens the editor with an explicitly blank id', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const props = renderFilters();
    await user.click(screen.getByText('添加渠道'));
    // `{ id: undefined }` is what tells EditChannelModal it is in create
    // mode. Passing the previously-edited channel here would silently turn
    // "add" into "overwrite".
    expect(props.setEditingChannel).toHaveBeenCalledWith({ id: undefined });
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });
});

describe('search form', () => {
  it('exposes the three documented search fields', () => {
    renderFilters();
    expect(screen.getByTestId('field-searchKeyword')).toHaveAttribute(
      'placeholder',
      '渠道ID，名称，密钥，API地址',
    );
    expect(screen.getByTestId('field-searchModel')).toBeInTheDocument();
    expect(screen.getByTestId('field-searchGroup')).toBeInTheDocument();
  });

  it('runs the search with the current tag mode on submit', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const props = renderFilters({ enableTagMode: true });
    await user.click(screen.getByText('查询'));
    expect(props.searchChannels).toHaveBeenCalledWith(true);
  });

  it('prepends a null "choose a group" entry to the group options', () => {
    renderFilters({ groupOptions: [{ label: 'vip', value: 'vip' }] });
    const opts = JSON.parse(
      screen.getByTestId('field-searchGroup').dataset.options,
    );
    expect(opts[0]).toEqual({ label: '选择分组', value: null });
    expect(opts[1]).toEqual({ label: 'vip', value: 'vip' });
  });

  it('defers the group search by a tick so the form value lands first', async () => {
    const props = renderFilters();
    screen.getByTestId('pick-searchGroup').click();
    // Searching synchronously here reads the PREVIOUS group and returns the
    // wrong rows; the deferral is the fix and must stay.
    expect(props.searchChannels).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(0);
    expect(props.searchChannels).toHaveBeenCalledWith(false);
  });

  it('shows the submit button busy while either load is in flight', () => {
    renderFilters({ loading: true, searching: false });
    expect(screen.getByText('查询').dataset.loading).toBe('true');
  });

  it('shows the submit button idle when nothing is in flight', () => {
    renderFilters({ loading: false, searching: false });
    expect(screen.getByText('查询').dataset.loading).toBe('false');
  });

  it('seeds the form from formInitValues', () => {
    renderFilters({
      formInitValues: {
        searchKeyword: 'azure',
        searchGroup: 'vip',
        searchModel: '',
      },
    });
    expect(JSON.parse(screen.getByTestId('form').dataset.init)).toEqual({
      searchKeyword: 'azure',
      searchGroup: 'vip',
      searchModel: '',
    });
  });
});

describe('reset', () => {
  it('clears the form and only then reloads', async () => {
    const formApi = { reset: vi.fn() };
    const props = renderFilters({ formApi });

    screen.getByText('重置').click();
    expect(formApi.reset).toHaveBeenCalledTimes(1);
    // Refreshing before the reset lands would re-run the old query and leave
    // the operator staring at filtered rows under a cleared form.
    expect(props.refresh).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(100);
    expect(props.refresh).toHaveBeenCalledTimes(1);
  });

  it('does nothing at all when the form api has not been handed over yet', async () => {
    const props = renderFilters({ formApi: null });
    screen.getByText('重置').click();
    await vi.advanceTimersByTimeAsync(200);
    expect(props.refresh).not.toHaveBeenCalled();
  });
});

describe('other entry points', () => {
  it('refreshes on demand', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const props = renderFilters();
    await user.click(screen.getByText('刷新'));
    expect(props.refresh).toHaveBeenCalledTimes(1);
  });

  it('opens the column selector', async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    const props = renderFilters();
    await user.click(screen.getByText('列设置'));
    expect(props.setShowColumnSelector).toHaveBeenCalledWith(true);
  });
});
