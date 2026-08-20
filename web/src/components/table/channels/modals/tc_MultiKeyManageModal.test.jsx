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

// This modal manages a channel's upstream provider keys. Two contracts matter
// more than anything else here:
//   1. NO KEY MATERIAL REACHES THE DOM. The `key_preview` column is commented
//      out upstream; if someone uncomments it, or a key leaks in through
//      another field, the assertions below go red. That is the point.
//   2. Destructive key operations hit the right endpoint with the right
//      channel and index, and a failure is reported as a failure — a delete
//      that silently no-ops is how an operator ends up believing a
//      compromised key is gone when it is still live upstream.
//
// The Table stub renders real rows through the real column render functions,
// so the columns are exercised rather than skipped.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  post: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
}));

vi.mock('../../../../helpers', () => ({
  API: { post: H.post },
  showError: H.showError,
  showSuccess: H.showSuccess,
  // Real-ish: the disabled-time column must render something a human can
  // read, so return a recognisable formatted string rather than null.
  timestamp2string: (ts) => `formatted(${ts})`,
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () =>
    React.createElement('i', { 'data-testid': 'illo' }),
  IllustrationNoResultDark: () =>
    React.createElement('i', { 'data-testid': 'illo-dark' }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const passthrough =
    (testid) =>
    ({ children }) =>
      React.createElement('div', { 'data-testid': testid }, children);

  const Modal = ({ visible, title, children }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal', role: 'dialog' },
          React.createElement('div', { 'data-testid': 'modal-title' }, title),
          children,
        )
      : null;

  const Button = ({ children, onClick, loading, disabled, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        disabled: disabled || loading,
        'data-loading': loading ? 'true' : 'false',
        ...rest,
      },
      children,
    );

  // Popconfirm must NOT auto-confirm: the whole assertion "nothing is deleted
  // until the operator confirms" depends on onConfirm being separately
  // reachable. Render the trigger plus an explicit confirm button carrying
  // the dialog copy, so both are observable.
  const Popconfirm = ({ title, content, onConfirm, children }) =>
    React.createElement(
      'span',
      { 'data-testid': 'popconfirm', 'data-title': title },
      children,
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'popconfirm-ok',
          'data-for': title,
          'data-content': content,
          onClick: onConfirm,
        },
        'confirm',
      ),
    );

  const Tag = ({ children, color, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color, ...rest },
      children,
    );

  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);

  const Table = ({ columns, dataSource, title, empty }) =>
    React.createElement(
      'div',
      { 'data-testid': 'table' },
      typeof title === 'function' ? title() : title,
      (dataSource || []).length === 0
        ? empty
        : (dataSource || []).map((record, rowIdx) =>
            React.createElement(
              'div',
              { key: record.index ?? rowIdx, 'data-testid': 'key-row' },
              columns.map((col, colIdx) =>
                React.createElement(
                  'span',
                  { key: colIdx, 'data-col': col.dataIndex ?? col.key },
                  col.render
                    ? col.render(record[col.dataIndex], record, rowIdx)
                    : record[col.dataIndex],
                ),
              ),
            ),
          ),
    );

  const Select = ({ value, onChange, children, placeholder }) =>
    React.createElement(
      'div',
      { 'data-testid': 'status-filter', 'data-value': String(value) },
      React.createElement('span', null, placeholder),
      React.Children.map(children, (child, i) =>
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `filter-opt-${i}`,
            onClick: () => onChange(child.props.value),
          },
          child.props.children,
        ),
      ),
    );
  Select.Option = ({ children }) => React.createElement('span', null, children);

  return {
    Modal,
    Button,
    Table,
    Tag,
    Typography: { Text },
    Space: passthrough('space'),
    Tooltip: ({ content, children }) =>
      React.createElement(
        'span',
        {
          'data-testid': 'tooltip',
          'data-content': typeof content === 'string' ? content : undefined,
        },
        children,
      ),
    Popconfirm,
    Empty: ({ title, description }) =>
      React.createElement(
        'div',
        { 'data-testid': 'empty' },
        title,
        description,
      ),
    Spin: passthrough('spin'),
    Select,
    Row: passthrough('row'),
    Col: passthrough('col'),
    Badge: ({ type }) =>
      React.createElement('i', { 'data-testid': 'badge', 'data-type': type }),
    // Progress is a number on screen: surface the percent so the stats block
    // is verifiable instead of silently rendering nothing.
    Progress: ({ percent, stroke }) =>
      React.createElement('div', {
        'data-testid': 'progress',
        'data-percent': String(percent),
        'data-stroke': stroke,
      }),
    Card: passthrough('card'),
  };
});

import MultiKeyManageModal from './MultiKeyManageModal';

const CHANNEL = {
  id: 42,
  name: 'openai-pool',
  channel_info: { is_multi_key: true, multi_key_mode: 'polling' },
};

const okPayload = (over = {}) => ({
  data: {
    success: true,
    data: {
      keys: [
        { index: 0, status: 1 },
        {
          index: 1,
          status: 3,
          reason: 'upstream 401',
          disabled_time: 1750000000,
        },
      ],
      total: 2,
      page: 1,
      page_size: 10,
      total_pages: 1,
      enabled_count: 1,
      manual_disabled_count: 0,
      auto_disabled_count: 1,
      ...over,
    },
  },
});

const renderModal = (props = {}) => {
  const merged = {
    visible: true,
    onCancel: vi.fn(),
    channel: CHANNEL,
    onRefresh: vi.fn(),
    ...props,
  };
  const view = render(<MultiKeyManageModal {...merged} />);
  return { ...view, props: merged };
};

const lastPostBody = () => H.post.mock.calls[H.post.mock.calls.length - 1][1];
const confirmFor = (title) =>
  screen.getAllByTestId('popconfirm-ok').find((b) => b.dataset.for === title);

beforeEach(() => {
  vi.clearAllMocks();
  H.post.mockResolvedValue(okPayload());
});

describe('key material never reaches the DOM', () => {
  it('renders index and status but no key text, even when the API returns key fields', async () => {
    // The server payload deliberately carries key-ish fields. If a future
    // change starts rendering any of them, this goes red.
    H.post.mockResolvedValue(
      okPayload({
        keys: [
          {
            index: 0,
            status: 1,
            key: 'sk-live-SUPERSECRET-000',
            key_preview: 'sk-live...000',
          },
        ],
        total: 1,
        enabled_count: 1,
        auto_disabled_count: 0,
      }),
    );
    renderModal();
    await screen.findByTestId('key-row');

    const dom = document.body.innerHTML;
    expect(dom).not.toContain('SUPERSECRET');
    expect(dom).not.toContain('sk-live');
    // ...and it is not an empty render either: the index is on screen.
    expect(screen.getByText('#0')).toBeInTheDocument();
  });

  it('never sends the key back to the server on a disable', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');

    await user.click(screen.getByText('禁用'));
    // mount-load, disable_key, then the post-write reload.
    await waitFor(() => expect(H.post).toHaveBeenCalledTimes(3));
    const body = H.post.mock.calls[1][1];
    expect(body).toEqual({
      channel_id: 42,
      action: 'disable_key',
      key_index: 0,
    });
    expect(JSON.stringify(body)).not.toMatch(/sk-/);
  });

  it('disables the key the operator clicked, not whichever is first', async () => {
    // The fixture above only ever has key #0 enabled, so `key_index: keyIndex`
    // and a hard-coded `key_index: 0` produce identical requests and the test
    // cannot tell them apart. Put the only enabled key at a non-zero index so
    // the index actually has to travel from the row to the request body —
    // otherwise "disable key #7" silently disables someone else's key #0.
    H.post.mockResolvedValue(
      okPayload({
        keys: [
          { index: 0, status: 3, reason: 'upstream 401', disabled_time: 1 },
          { index: 7, status: 1 },
        ],
        total: 2,
        enabled_count: 1,
        manual_disabled_count: 0,
        auto_disabled_count: 1,
      }),
    );
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');

    await user.click(screen.getByText('禁用'));
    await waitFor(() => expect(H.post).toHaveBeenCalledTimes(3));
    expect(H.post.mock.calls[1][1]).toEqual({
      channel_id: 42,
      action: 'disable_key',
      key_index: 7,
    });
  });
});

describe('loading key status', () => {
  it('requests page 1 for the channel when the modal opens', async () => {
    renderModal();
    await waitFor(() => expect(H.post).toHaveBeenCalledTimes(1));
    expect(H.post).toHaveBeenCalledWith('/api/channel/multi_key/manage', {
      channel_id: 42,
      action: 'get_key_status',
      page: 1,
      page_size: 10,
    });
  });

  it('requests nothing at all while the modal is closed', () => {
    renderModal({ visible: false });
    expect(H.post).not.toHaveBeenCalled();
    expect(screen.queryByTestId('modal')).toBeNull();
  });

  it('requests nothing when there is no channel to manage', () => {
    renderModal({ channel: null });
    expect(H.post).not.toHaveBeenCalled();
  });

  it('surfaces the server message when the request is refused', async () => {
    H.post.mockResolvedValue({
      data: { success: false, message: 'channel not found' },
    });
    renderModal();
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('channel not found'),
    );
    expect(screen.getByTestId('empty')).toBeInTheDocument();
  });

  it('reports a thrown request as a failure rather than an empty key list', async () => {
    H.post.mockRejectedValue(new Error('network down'));
    renderModal();
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('获取密钥状态失败'),
    );
  });
});

describe('key status rendering', () => {
  it('labels each key status and shows the reason only for disabled keys', async () => {
    renderModal();
    const rows = await screen.findAllByTestId('key-row');
    expect(rows).toHaveLength(2);

    const enabled = rows[0];
    expect(enabled.textContent).toContain('已启用');
    // An enabled key has no disable reason and no disable time — the column
    // must collapse to a dash, not print a stale reason.
    expect(enabled.textContent).not.toContain('upstream 401');

    const autoDisabled = rows[1];
    expect(autoDisabled.textContent).toContain('自动禁用');
    expect(autoDisabled.textContent).toContain('upstream 401');
    expect(autoDisabled.textContent).toContain('formatted(1750000000)');
  });

  it('does not print a disable reason for a key the server marked enabled', async () => {
    // Negative half of the pair above: same reason field, status 1.
    H.post.mockResolvedValue(
      okPayload({
        keys: [
          { index: 0, status: 1, reason: 'stale reason', disabled_time: 1 },
        ],
        total: 1,
      }),
    );
    renderModal();
    const row = await screen.findByTestId('key-row');
    expect(row.textContent).not.toContain('stale reason');
    expect(row.textContent).not.toContain('formatted(1)');
  });

  it('renders the enabled/disabled split as percentages of the total', async () => {
    H.post.mockResolvedValue(
      okPayload({
        total: 4,
        enabled_count: 1,
        manual_disabled_count: 1,
        auto_disabled_count: 2,
      }),
    );
    renderModal();
    await screen.findAllByTestId('key-row');
    const percents = screen
      .getAllByTestId('progress')
      .map((n) => n.dataset.percent);
    expect(percents).toEqual(['25', '25', '50']);
  });

  it('shows 0% rather than NaN% when the channel has no keys at all', async () => {
    H.post.mockResolvedValue(
      okPayload({
        keys: [],
        total: 0,
        enabled_count: 0,
        manual_disabled_count: 0,
        auto_disabled_count: 0,
      }),
    );
    renderModal();
    await screen.findByTestId('empty');
    for (const p of screen.getAllByTestId('progress')) {
      expect(p.dataset.percent).toBe('0');
      expect(p.dataset.percent).not.toMatch(/NaN/);
    }
  });
});

describe('single-key operations', () => {
  it('enables a disabled key and refreshes both the list and the parent', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await screen.findAllByTestId('key-row');

    await user.click(screen.getByText('启用'));
    await waitFor(() =>
      expect(H.showSuccess).toHaveBeenCalledWith('密钥已启用'),
    );
    expect(H.post.mock.calls[1][1]).toEqual({
      channel_id: 42,
      action: 'enable_key',
      key_index: 1,
    });
    // The list must be re-read, and the channel table behind it too.
    expect(H.post).toHaveBeenCalledTimes(3);
    expect(props.onRefresh).toHaveBeenCalledTimes(1);
  });

  it('does not claim success when enabling a key is refused', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await screen.findAllByTestId('key-row');

    H.post.mockResolvedValueOnce({
      data: { success: false, message: 'key already enabled' },
    });
    await user.click(screen.getByText('启用'));
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('key already enabled'),
    );
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.onRefresh).not.toHaveBeenCalled();
  });

  it('does not claim success when disabling a key throws', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');

    H.post.mockRejectedValueOnce(new Error('boom'));
    await user.click(screen.getByText('禁用'));
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('禁用密钥失败'),
    );
    expect(H.showSuccess).not.toHaveBeenCalled();
  });
});

describe('destructive operations are confirmed and scoped', () => {
  it('warns that a single key delete is permanent before deleting it', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');
    H.post.mockClear();

    const ok = confirmFor('确定要删除此密钥吗？');
    expect(ok.dataset.content).toBe('此操作不可撤销，将永久删除该密钥');
    // Rendering the row must not have deleted anything.
    expect(H.post).not.toHaveBeenCalled();

    await user.click(ok);
    await waitFor(() =>
      expect(H.showSuccess).toHaveBeenCalledWith('密钥已删除'),
    );
    expect(H.post.mock.calls[0][1]).toEqual({
      channel_id: 42,
      action: 'delete_key',
      key_index: 0,
    });
  });

  it('scopes the bulk prune to auto-disabled keys, matching its own warning', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');
    H.post.mockClear();

    const ok = confirmFor('确定要删除所有已自动禁用的密钥吗？');
    expect(ok.dataset.content).toBe(
      '此操作不可撤销，将永久删除已自动禁用的密钥',
    );
    await user.click(ok);

    await waitFor(() => expect(H.post).toHaveBeenCalled());
    // The action name is the contract with the Go handler, which prunes only
    // auto-disabled keys. A rename to a broader action here would silently
    // widen an irreversible delete.
    expect(H.post.mock.calls[0][1]).toEqual({
      channel_id: 42,
      action: 'delete_disabled_keys',
    });
  });

  it('enables every key in one call and returns to the first page', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');
    H.post.mockClear();

    await user.click(confirmFor('确定要启用所有密钥吗？'));
    await waitFor(() => expect(H.post).toHaveBeenCalled());
    expect(H.post.mock.calls[0][1]).toEqual({
      channel_id: 42,
      action: 'enable_all_keys',
    });
    await waitFor(() =>
      expect(lastPostBody()).toMatchObject({
        action: 'get_key_status',
        page: 1,
      }),
    );
  });

  it('disables every key in one call', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');
    H.post.mockClear();

    await user.click(confirmFor('确定要禁用所有的密钥吗？'));
    await waitFor(() => expect(H.post).toHaveBeenCalled());
    expect(H.post.mock.calls[0][1]).toEqual({
      channel_id: 42,
      action: 'disable_all_keys',
    });
  });

  it('hides "enable all" when nothing is disabled and "disable all" when nothing is enabled', async () => {
    H.post.mockResolvedValue(
      okPayload({
        keys: [{ index: 0, status: 1 }],
        total: 1,
        enabled_count: 1,
        manual_disabled_count: 0,
        auto_disabled_count: 0,
      }),
    );
    renderModal();
    await screen.findByTestId('key-row');
    expect(confirmFor('确定要启用所有密钥吗？')).toBeUndefined();
    expect(confirmFor('确定要禁用所有的密钥吗？')).toBeDefined();
  });

  it('does not claim a bulk delete succeeded when the server refuses it', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await screen.findAllByTestId('key-row');

    H.post.mockResolvedValueOnce({
      data: { success: false, message: 'locked by another admin' },
    });
    await user.click(confirmFor('确定要删除所有已自动禁用的密钥吗？'));
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('locked by another admin'),
    );
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.onRefresh).not.toHaveBeenCalled();
  });
});

describe('status filter', () => {
  it('sends the chosen status and resets to the first page', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');
    H.post.mockClear();

    // Option order: all(null), 1 enabled, 2 manual, 3 auto.
    await user.click(screen.getByTestId('filter-opt-3'));
    await waitFor(() => expect(H.post).toHaveBeenCalled());
    expect(H.post.mock.calls[0][1]).toEqual({
      channel_id: 42,
      action: 'get_key_status',
      page: 1,
      page_size: 10,
      status: 3,
    });
  });

  it('omits the status field entirely when the filter is cleared to "all"', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findAllByTestId('key-row');

    await user.click(screen.getByTestId('filter-opt-3'));
    await waitFor(() => expect(lastPostBody().status).toBe(3));

    H.post.mockClear();
    await user.click(screen.getByTestId('filter-opt-0'));
    await waitFor(() => expect(H.post).toHaveBeenCalled());
    // A lingering status would silently hide keys from the operator.
    expect(H.post.mock.calls[0][1]).not.toHaveProperty('status');
  });
});

describe('modal header', () => {
  it('names the channel, its key count and its dispatch mode', async () => {
    renderModal();
    await screen.findAllByTestId('key-row');
    const title = screen.getByTestId('modal-title').textContent;
    expect(title).toContain('openai-pool');
    expect(title).toContain('总密钥数');
    expect(title).toContain('2');
    expect(title).toContain('轮询模式');
    expect(title).not.toContain('随机模式');
  });

  it('names random mode for a random-dispatch pool', async () => {
    renderModal({
      channel: { ...CHANNEL, channel_info: { multi_key_mode: 'random' } },
    });
    await screen.findAllByTestId('key-row');
    const title = screen.getByTestId('modal-title').textContent;
    expect(title).toContain('随机模式');
    expect(title).not.toContain('轮询模式');
  });
});
