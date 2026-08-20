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
import {
  render,
  screen,
  fireEvent,
  waitFor,
  within,
} from '@testing-library/react';

// Semi-UI is stubbed: its barrel reaches semi-illustrations -> lottie-web ->
// HTMLCanvasElement.getContext, which jsdom does not implement (the Flows page
// test stubs it for the same reason). The Table stub runs every column
// render() and every column title node, which is where this page keeps its
// per-upstream checkbox and bulk-select logic.
vi.mock('@douyinfe/semi-ui', () => {
  const cellKey = (col, idx) => String(col.key ?? col.dataIndex ?? idx);

  const Table = ({ columns = [], dataSource = [], loading }) =>
    React.createElement(
      'div',
      { 'data-testid': 'table', 'data-loading': String(!!loading) },
      React.createElement(
        'div',
        null,
        columns.map((col, i) =>
          React.createElement(
            'span',
            { key: i, 'data-testid': `th-${cellKey(col, i)}` },
            col.title,
          ),
        ),
      ),
      dataSource.map((record, rowIdx) =>
        React.createElement(
          'div',
          { key: record.key ?? rowIdx, 'data-testid': `row-${record.key}` },
          columns.map((col, i) =>
            React.createElement(
              'span',
              {
                key: i,
                'data-testid': `cell-${record.key}-${cellKey(col, i)}`,
              },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
        ),
      ),
    );

  const Button = ({ children, onClick, disabled, icon: _i, ...rest }) =>
    React.createElement(
      'button',
      { onClick, disabled: !!disabled, ...rest },
      children,
    );

  const Input = ({
    value,
    onChange,
    placeholder,
    prefix: _p,
    showClear: _s,
    ...rest
  }) =>
    React.createElement('input', {
      type: 'text',
      value: value ?? '',
      placeholder,
      onChange: (e) => onChange && onChange(e.target.value),
      ...rest,
    });

  const Modal = ({ visible, title, children, onOk, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'conflict-modal' },
          React.createElement(
            'span',
            { 'data-testid': 'conflict-title' },
            title,
          ),
          children,
          React.createElement(
            'button',
            { 'data-testid': 'conflict-ok', onClick: onOk },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'conflict-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null;

  const Form = {
    Section: ({ text, children }) =>
      React.createElement('div', null, text, children),
  };

  const Tag = ({ children }) =>
    React.createElement('span', { 'data-testid': 'tag' }, children);

  const Empty = ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description);

  const Checkbox = ({ checked, indeterminate, onChange, children }) =>
    React.createElement(
      'label',
      null,
      React.createElement('input', {
        type: 'checkbox',
        'data-testid': 'cbx',
        'data-indeterminate': String(!!indeterminate),
        checked: !!checked,
        onChange: (e) =>
          onChange && onChange({ target: { checked: e.target.checked } }),
      }),
      children,
    );

  // Surfaces `content` as an attribute. The stub used to drop it, which made
  // the billing-conflict warning invisible to the DOM: the branch ran and
  // counted toward coverage while nothing could assert it.
  const Tooltip = ({ children, content }) =>
    React.createElement(
      'span',
      typeof content === 'string' ? { 'data-tooltip': content } : null,
      children,
    );

  const Select = ({ value, onChange, children, placeholder }) =>
    React.createElement(
      'select',
      {
        'data-testid': 'ratio-type-filter',
        value: value ?? '',
        onChange: (e) => onChange && onChange(e.target.value),
      },
      React.createElement('option', { value: '' }, placeholder || 'any'),
      children,
    );
  Select.Option = ({ value, children }) =>
    React.createElement('option', { value }, children);

  return {
    Table,
    Button,
    Input,
    Modal,
    Form,
    Tag,
    Empty,
    Checkbox,
    Tooltip,
    Select,
  };
});

vi.mock('@douyinfe/semi-icons', () => ({ IconSearch: () => null }));
vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () => null,
  IllustrationNoResultDark: () => null,
}));
vi.mock('lucide-react', () => ({
  RefreshCcw: () => null,
  CheckSquare: () => null,
  // Renders an empty marker rather than null so the warning branch is
  // observable. Emits no text, so textContent assertions elsewhere are unaffected.
  AlertTriangle: () => React.createElement('i', { 'data-testid': 'warn-icon' }),
  CheckCircle: () => null,
}));

// Drive the channel picker straight from the props the page hands it: one
// button per channel toggles the selection, one confirms. Also mirrors back the
// endpoint map so the default-endpoint logic can be asserted.
vi.mock('../../../components/settings/ChannelSelectorModal', () => ({
  default: React.forwardRef((props, ref) => {
    React.useImperativeHandle(ref, () => ({ resetPagination: () => {} }));
    if (!props.visible) return null;
    return React.createElement(
      'div',
      { 'data-testid': 'channel-picker' },
      React.createElement(
        'span',
        { 'data-testid': 'endpoints' },
        JSON.stringify(props.channelEndpoints),
      ),
      props.allChannels.map((ch) =>
        React.createElement(
          'button',
          {
            key: ch.value,
            'data-testid': `pick-${ch.value}`,
            onClick: () =>
              props.setSelectedChannelIds(
                props.selectedChannelIds.includes(ch.value)
                  ? props.selectedChannelIds.filter((v) => v !== ch.value)
                  : [...props.selectedChannelIds, ch.value],
              ),
          },
          ch.label,
        ),
      ),
      React.createElement(
        'button',
        { 'data-testid': 'picker-ok', onClick: props.onOk },
        'ok',
      ),
      React.createElement(
        'button',
        { 'data-testid': 'picker-cancel', onClick: props.onCancel },
        'cancel',
      ),
    );
  }),
}));

vi.mock('../../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => false,
}));

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  stringToColor: () => 'blue',
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import UpstreamRatioSync from './UpstreamRatioSync';
import { API, showError, showSuccess, showWarning } from '../../../helpers';

const putOk = () => Promise.resolve({ data: { success: true } });

const putPayload = (key) => {
  const call = API.put.mock.calls.find((c) => c[1].key === key);
  if (!call) return undefined;
  return JSON.parse(call[1].value);
};

const CHANNELS = [
  { id: 1, name: 'acme', base_url: 'https://acme.example' },
  { id: -100, name: '官方倍率预设', base_url: 'https://basellm.github.io' },
];

const channelsOk = (list = CHANNELS) =>
  Promise.resolve({ data: { success: true, data: list } });

const fetchOk = (differences, test_results = []) =>
  Promise.resolve({
    data: { success: true, data: { differences, test_results } },
  });

const LOCAL = {
  ModelRatio: '{"alpha-1":1}',
  CompletionRatio: '{"alpha-1":2}',
  CacheRatio: '{}',
  ModelPrice: '{}',
};

const mount = (options = LOCAL, refresh = vi.fn()) => {
  const utils = render(
    <UpstreamRatioSync options={options} refresh={refresh} />,
  );
  return { ...utils, refresh };
};

/** Open the picker, tick the given channel ids, confirm. */
const syncFrom = async (ids = [1]) => {
  fireEvent.click(screen.getByText('选择同步渠道'));
  await waitFor(() =>
    expect(screen.getByTestId('channel-picker')).toBeTruthy(),
  );
  await waitFor(() => expect(screen.getByTestId('pick-1')).toBeTruthy());
  ids.forEach((id) => fireEvent.click(screen.getByTestId(`pick-${id}`)));
  fireEvent.click(screen.getByTestId('picker-ok'));
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  showWarning.mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

// ─── Empty / pre-sync state ────────────────────────────────────────────────

describe('UpstreamRatioSync idle state', () => {
  it('asks for a channel before anything has been fetched', () => {
    mount();

    expect(screen.getByTestId('empty')).toHaveTextContent('请先选择同步渠道');
    expect(screen.getByText('应用同步').closest('button').disabled).toBe(true);
  });

  it('says the fetch found nothing once a sync has run', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => fetchOk({}));
    mount();

    await syncFrom([1]);

    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith('未找到差异化倍率，无需同步'),
    );
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无差异化倍率显示');
  });
});

// ─── Channel discovery + endpoint defaults ─────────────────────────────────

describe('UpstreamRatioSync channel discovery', () => {
  it('defaults the official preset and normal channels to different endpoints', async () => {
    API.get.mockImplementation(() => channelsOk());
    mount();

    fireEvent.click(screen.getByText('选择同步渠道'));
    await waitFor(() =>
      expect(API.get).toHaveBeenCalledWith('/api/ratio_sync/channels'),
    );
    await waitFor(() =>
      expect(screen.getByTestId('endpoints').textContent).not.toBe('{}'),
    );

    const endpoints = JSON.parse(screen.getByTestId('endpoints').textContent);
    expect(endpoints['1']).toBe('/api/ratio_config');
    expect(endpoints['-100']).toBe(
      '/llm-metadata/api/ailurus/ratio_config-v1-base.json',
    );
  });

  it('fetches the channel list only once across repeated opens', async () => {
    API.get.mockImplementation(() => channelsOk());
    mount();

    fireEvent.click(screen.getByText('选择同步渠道'));
    await waitFor(() => expect(screen.getByTestId('pick-1')).toBeTruthy());
    fireEvent.click(screen.getByTestId('picker-cancel'));
    fireEvent.click(screen.getByText('选择同步渠道'));

    await waitFor(() => expect(screen.getByTestId('pick-1')).toBeTruthy());
    expect(API.get).toHaveBeenCalledTimes(1);
  });

  it('reports a failed channel list', async () => {
    API.get.mockImplementation(() =>
      Promise.resolve({ data: { success: false, message: 'no channels' } }),
    );
    mount();

    fireEvent.click(screen.getByText('选择同步渠道'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('no channels'));
  });

  it('reports a thrown channel list', async () => {
    API.get.mockImplementation(() => Promise.reject(new Error('dns')));
    mount();

    fireEvent.click(screen.getByText('选择同步渠道'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('获取渠道失败：dns'),
    );
  });

  it('refuses to sync when no channel is ticked', async () => {
    API.get.mockImplementation(() => channelsOk());
    mount();

    fireEvent.click(screen.getByText('选择同步渠道'));
    await waitFor(() => expect(screen.getByTestId('pick-1')).toBeTruthy());
    fireEvent.click(screen.getByTestId('picker-ok'));

    expect(showWarning).toHaveBeenCalledWith('请至少选择一个渠道');
    expect(API.post).not.toHaveBeenCalled();
  });

  it('posts the per-channel endpoint with the fetch request', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => fetchOk({}));
    mount();

    await syncFrom([1]);

    await waitFor(() => expect(API.post).toHaveBeenCalled());
    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/ratio_sync/fetch');
    expect(body.timeout).toBe(10);
    expect(body.upstreams).toEqual([
      {
        id: 1,
        name: 'acme',
        base_url: 'https://acme.example',
        endpoint: '/api/ratio_config',
      },
    ]);
  });

  it('reports a rejected fetch and keeps the table empty', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      Promise.resolve({ data: { success: false, message: 'upstream 500' } }),
    );
    mount();

    await syncFrom([1]);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('upstream 500'));
    expect(screen.getByTestId('empty')).toBeTruthy();
  });

  it('reports a thrown fetch', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => Promise.reject(new Error('timeout')));
    mount();

    await syncFrom([1]);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('请求后端接口失败：timeout'),
    );
  });

  it('warns about the individual upstreams that failed', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({}, [
        { name: 'acme', status: 'error', error: '404' },
        { name: 'ok-one', status: 'success' },
      ]),
    );
    mount();

    await syncFrom([1]);

    await waitFor(() =>
      expect(showWarning).toHaveBeenCalledWith('部分渠道测试失败：acme: 404'),
    );
  });
});

// ─── Difference table rendering ────────────────────────────────────────────

// Built fresh per test on purpose: performSync's success path shallow-copies
// the differences state and then deletes from the *nested* object, so it
// mutates whatever object the fetch response handed it. A shared literal would
// leak state from one test into the next.
const DIFF_ONE = () => ({
  'alpha-1': {
    model_ratio: {
      current: 1,
      upstreams: { acme: 2, other: 'same' },
      confidence: { acme: true, other: true },
    },
  },
});

describe('UpstreamRatioSync difference table', () => {
  const arrive = async (diff) => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => fetchOk(diff));
    mount();
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());
  };

  it('renders one row per model/ratio-type pair with the local value', async () => {
    await arrive(DIFF_ONE());

    const row = screen.getByTestId('row-alpha-1_model_ratio');
    expect(
      within(row).getByTestId('cell-alpha-1_model_ratio-model'),
    ).toHaveTextContent('alpha-1');
    expect(
      within(row).getByTestId('cell-alpha-1_model_ratio-ratioType'),
    ).toHaveTextContent('模型倍率');
    expect(
      within(row).getByTestId('cell-alpha-1_model_ratio-current'),
    ).toHaveTextContent('1');
  });

  it('labels a missing local value as unset rather than 0', async () => {
    await arrive({
      neu: {
        cache_ratio: {
          current: null,
          upstreams: { acme: 0.5 },
          confidence: {},
        },
      },
    });

    expect(
      screen.getByTestId('cell-neu_cache_ratio-current'),
    ).toHaveTextContent('未设置');
  });

  it('marks an upstream that already matches local as "same" and offers no checkbox', async () => {
    await arrive(DIFF_ONE());

    const sameCell = screen.getByTestId('cell-alpha-1_model_ratio-other');
    expect(sameCell).toHaveTextContent('与本地相同');
    expect(within(sameCell).queryByTestId('cbx')).toBeNull();
  });

  it('renders an unset upstream cell without a checkbox', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: {
          current: 1,
          upstreams: { acme: 2, missing: null },
          confidence: {},
        },
      },
    });

    const cell = screen.getByTestId('cell-alpha-1_model_ratio-missing');
    expect(cell).toHaveTextContent('未设置');
    expect(within(cell).queryByTestId('cbx')).toBeNull();
  });

  it('downgrades the confidence badge when an upstream is flagged', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: {
          current: 1,
          upstreams: { acme: 2 },
          confidence: { acme: false },
        },
      },
    });

    expect(
      screen.getByTestId('cell-alpha-1_model_ratio-confidence'),
    ).toHaveTextContent('谨慎');
  });

  it('shows the trusted badge when every upstream is confident', async () => {
    await arrive(DIFF_ONE());

    expect(
      screen.getByTestId('cell-alpha-1_model_ratio-confidence'),
    ).toHaveTextContent('可信');
  });

  it('warns on the row when one model carries both a price and a ratio diff', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        model_price: {
          current: null,
          upstreams: { acme: 0.01 },
          confidence: {},
        },
      },
    });

    // Both rows of that model are tagged, because the conflict is per-model,
    // not per-row. Assert the warning itself, not merely that the rows exist:
    // the rows render either way, so a row-existence check passes even with the
    // whole warning feature switched off.
    const ratioRow = screen.getByTestId('row-alpha-1_model_ratio');
    const priceRow = screen.getByTestId('row-alpha-1_model_price');
    expect(within(ratioRow).getByTestId('warn-icon')).toBeInTheDocument();
    expect(within(priceRow).getByTestId('warn-icon')).toBeInTheDocument();
    expect(
      within(ratioRow).getByTestId('warn-icon').parentElement,
    ).toHaveAttribute(
      'data-tooltip',
      '该模型存在固定价格与倍率计费方式冲突，请确认选择',
    );
  });

  it('does not warn when the model only has a ratio diff', async () => {
    await arrive(DIFF_ONE());

    // Negative half of the pair. Without this, the assertion above cannot
    // distinguish "warns on conflict" from "always warns".
    expect(
      within(screen.getByTestId('row-alpha-1_model_ratio')).queryByTestId(
        'warn-icon',
      ),
    ).toBeNull();
  });

  it('filters rows by model keyword', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
      },
      'beta-1': {
        model_ratio: { current: 1, upstreams: { acme: 3 }, confidence: {} },
      },
    });

    fireEvent.change(screen.getByPlaceholderText('搜索模型名称'), {
      target: { value: 'BETA' },
    });

    await waitFor(() =>
      expect(screen.getByTestId('row-beta-1_model_ratio')).toBeTruthy(),
    );
    expect(screen.queryByTestId('row-alpha-1_model_ratio')).toBeNull();
  });

  it('shows the no-match slot when the keyword matches nothing', async () => {
    await arrive(DIFF_ONE());

    fireEvent.change(screen.getByPlaceholderText('搜索模型名称'), {
      target: { value: 'zzz' },
    });

    await waitFor(() =>
      expect(screen.getByTestId('empty')).toHaveTextContent('未找到匹配的模型'),
    );
  });

  it('filters rows by ratio type', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        completion_ratio: {
          current: 2,
          upstreams: { acme: 4 },
          confidence: {},
        },
      },
    });

    fireEvent.change(screen.getByTestId('ratio-type-filter'), {
      target: { value: 'completion_ratio' },
    });

    await waitFor(() =>
      expect(screen.queryByTestId('row-alpha-1_model_ratio')).toBeNull(),
    );
    expect(screen.getByTestId('row-alpha-1_completion_ratio')).toBeTruthy();
  });
});

// ─── Applying a sync ───────────────────────────────────────────────────────

describe('UpstreamRatioSync apply', () => {
  const arrive = async (diff, options = LOCAL) => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => fetchOk(diff));
    API.put.mockImplementation(putOk);
    const r = mount(options);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());
    return r;
  };

  const tick = (rowKey, upstream) =>
    fireEvent.click(
      within(screen.getByTestId(`cell-${rowKey}-${upstream}`)).getByTestId(
        'cbx',
      ),
    );

  it('merges the picked value into the stored map without dropping other models', async () => {
    const { refresh } = await arrive(
      {
        'alpha-1': {
          model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        },
      },
      {
        ModelRatio: '{"alpha-1":1,"untouched":9}',
        CompletionRatio: '{"alpha-1":2}',
        CacheRatio: '{}',
        ModelPrice: '{}',
      },
    );

    tick('alpha-1_model_ratio', 'acme');
    await waitFor(() =>
      expect(screen.getByText('应用同步').closest('button').disabled).toBe(
        false,
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('同步成功'));
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 2, untouched: 9 });
    expect(putPayload('CompletionRatio')).toEqual({ 'alpha-1': 2 });
    expect(refresh).toHaveBeenCalled();
  });

  it('writes all four ratio options on every apply', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
      },
    });

    tick('alpha-1_model_ratio', 'acme');
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(API.put).toHaveBeenCalledTimes(4));
    expect(API.put.mock.calls.map((c) => c[1].key).sort()).toEqual([
      'CacheRatio',
      'CompletionRatio',
      'ModelPrice',
      'ModelRatio',
    ]);
  });

  it('clears the applied row from the difference table', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
      },
    });

    tick('alpha-1_model_ratio', 'acme');
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByTestId('row-alpha-1_model_ratio')).toBeNull(),
    );
    expect(screen.getByText('应用同步').closest('button').disabled).toBe(true);
  });

  it('unticking a value removes it from the pending set', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
      },
    });

    tick('alpha-1_model_ratio', 'acme');
    await waitFor(() =>
      expect(screen.getByText('应用同步').closest('button').disabled).toBe(
        false,
      ),
    );
    tick('alpha-1_model_ratio', 'acme');

    await waitFor(() =>
      expect(screen.getByText('应用同步').closest('button').disabled).toBe(
        true,
      ),
    );
  });

  it('keeps price and ratio mutually exclusive within one model', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        model_price: {
          current: null,
          upstreams: { acme: 0.01 },
          confidence: {},
        },
      },
    });

    tick('alpha-1_model_ratio', 'acme');
    tick('alpha-1_model_price', 'acme');
    fireEvent.click(screen.getByText('应用同步'));

    // local alpha-1 is ratio-billed, so moving it to a fixed price is a
    // category change and must be confirmed first
    await waitFor(() =>
      expect(screen.getByTestId('conflict-modal')).toBeTruthy(),
    );
    fireEvent.click(screen.getByTestId('conflict-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    // picking the price last must retire the ratio selection, not merge both
    expect(putPayload('ModelPrice')).toEqual({ 'alpha-1': 0.01 });
    expect(putPayload('ModelRatio')).toEqual({});
    expect(putPayload('CompletionRatio')).toEqual({});
  });

  it('bulk-selects every selectable row of one upstream column', async () => {
    await arrive({
      'alpha-1': {
        model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
      },
      'beta-1': {
        model_ratio: { current: 1, upstreams: { acme: 3 }, confidence: {} },
      },
    });

    fireEvent.click(within(screen.getByTestId('th-acme')).getByTestId('cbx'));
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 2, 'beta-1': 3 });
  });

  it('reports a partially failed write and keeps the selection for a retry', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        },
      }),
    );
    API.put.mockImplementation((_url, body) =>
      body.key === 'CacheRatio'
        ? Promise.resolve({ data: { success: false, message: 'x' } })
        : putOk(),
    );
    const { refresh } = mount();
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('部分保存失败'));
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
    // the row stays on screen so the operator can retry
    expect(screen.getByTestId('row-alpha-1_model_ratio')).toBeTruthy();
  });

  it('reports a thrown write', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        },
      }),
    );
    API.put.mockImplementation(() => Promise.reject(new Error('net')));
    mount();
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('保存失败'));
  });
});

// ─── Cross-category conflict confirmation ──────────────────────────────────

describe('UpstreamRatioSync billing-category conflict', () => {
  const PRICED_LOCAL = {
    ModelRatio: '{}',
    CompletionRatio: '{}',
    CacheRatio: '{}',
    ModelPrice: '{"alpha-1":0.02}',
  };
  const RATIO_DIFF = () => ({
    'alpha-1': {
      model_ratio: { current: null, upstreams: { acme: 5 }, confidence: {} },
    },
  });

  const arriveConflict = async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() => fetchOk(RATIO_DIFF()));
    API.put.mockImplementation(putOk);
    const r = mount(PRICED_LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());
    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));
    return r;
  };

  it('stops for confirmation before switching a model off fixed pricing', async () => {
    await arriveConflict();

    await waitFor(() =>
      expect(screen.getByTestId('conflict-modal')).toBeTruthy(),
    );
    expect(screen.getByTestId('conflict-title')).toHaveTextContent(
      '确认冲突项修改',
    );
    expect(API.put).not.toHaveBeenCalled();
  });

  it('describes the current and the incoming billing shape, and the source channel', async () => {
    await arriveConflict();
    await waitFor(() => screen.getByTestId('conflict-modal'));

    const modal = screen.getByTestId('conflict-modal');
    expect(modal).toHaveTextContent('固定价格 : 0.02');
    expect(modal).toHaveTextContent('模型倍率 : 5');
    expect(modal).toHaveTextContent('acme');
  });

  it('applies and retires the fixed price only after confirmation', async () => {
    await arriveConflict();
    await waitFor(() => screen.getByTestId('conflict-modal'));

    fireEvent.click(screen.getByTestId('conflict-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('同步成功'));
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 5 });
    expect(putPayload('ModelPrice')).toEqual({});
  });

  it('writes nothing when the confirmation is dismissed', async () => {
    await arriveConflict();
    await waitFor(() => screen.getByTestId('conflict-modal'));

    fireEvent.click(screen.getByTestId('conflict-cancel'));

    expect(API.put).not.toHaveBeenCalled();
    expect(screen.queryByTestId('conflict-modal')).toBeNull();
    // the selection survives, so the operator can reconsider and re-apply
    expect(screen.getByText('应用同步').closest('button').disabled).toBe(false);
  });

  it('does not ask for confirmation when the category is unchanged', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: { current: 1, upstreams: { acme: 5 }, confidence: {} },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount(LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('同步成功'));
    expect(screen.queryByTestId('conflict-modal')).toBeNull();
  });

  it('does not ask for confirmation for a model that has no local pricing yet', async () => {
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        brandnew: {
          model_price: {
            current: null,
            upstreams: { acme: 0.5 },
            confidence: {},
          },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount(LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-brandnew_model_price-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('同步成功'));
    expect(screen.queryByTestId('conflict-modal')).toBeNull();
    expect(putPayload('ModelPrice')).toEqual({ brandnew: 0.5 });
  });

  it.skip('should not lose another column selections on bulk-deselect (DEFECT)', async () => {
    // handleBulkSelect(false) walks every visible row and deletes
    // resolutions[model][ratioType] without checking which upstream that value
    // came from. Unticking upstream A header therefore also discards choices
    // the operator made in upstream B column, silently, with no undo.
    // Un-skip once the bulk clear only removes values equal to that upstream.
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: {
            current: 1,
            upstreams: { acme: 2, other: 3 },
            confidence: {},
          },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount(LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    // choose the value coming from `other`
    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-other')).getByTestId(
        'cbx',
      ),
    );
    // now tick and untick the unrelated `acme` header checkbox
    fireEvent.click(within(screen.getByTestId('th-acme')).getByTestId('cbx'));
    fireEvent.click(within(screen.getByTestId('th-acme')).getByTestId('cbx'));

    fireEvent.click(screen.getByText('应用同步'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 3 });
  });

  it.skip('should handle a ratio type it does not map to an option key (DEFECT)', async () => {
    // performSync turns `audio_ratio` into the option key `AudioRatio`, but
    // finalRatios only holds ModelRatio/CompletionRatio/CacheRatio/ModelPrice.
    // finalRatios['AudioRatio'][model] = ... therefore throws a TypeError, and
    // because that line sits *outside* the try block the rejection is never
    // caught: no toast, no loading reset, the click just does nothing.
    // AudioRatio/ImageRatio/AudioCompletionRatio are real option keys in this
    // repo (see RatioSetting inputs), so this is reachable the moment the
    // backend reports a diff for one of them.
    // Un-skip once unknown ratio types are either mapped or rejected.
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          audio_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount(LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_audio_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
  });

  it.skip('should report malformed stored ratio JSON on apply (DEFECT)', async () => {
    // applySync (and the conflict modal onOk) call JSON.parse(props.options.*)
    // outside any try/catch. One malformed stored option makes 应用同步 throw
    // an uncaught rejection: the operator sees a dead button and no message.
    // Un-skip once the parse is guarded.
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: { current: 1, upstreams: { acme: 2 }, confidence: {} },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount({
      ModelRatio: '{"alpha-1":',
      CompletionRatio: '{}',
      CacheRatio: '{}',
      ModelPrice: '{}',
    });
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
  });

  it.skip('should not store a non-numeric upstream value as null (DEFECT)', async () => {
    // performSync does parseFloat(value) on whatever the upstream sent, with no
    // validation. A non-numeric upstream value becomes NaN, and JSON.stringify
    // serialises NaN as literal null, so {"alpha-1": null} is written into
    // ModelRatio. Negative and absurdly large upstream values are equally
    // unchecked.
    // Un-skip once upstream values are validated before the PUT.
    API.get.mockImplementation(() => channelsOk());
    API.post.mockImplementation(() =>
      fetchOk({
        'alpha-1': {
          model_ratio: {
            current: 1,
            upstreams: { acme: 'N/A' },
            confidence: {},
          },
        },
      }),
    );
    API.put.mockImplementation(putOk);
    mount(LOCAL);
    await syncFrom([1]);
    await waitFor(() => expect(screen.getByTestId('table')).toBeTruthy());

    fireEvent.click(
      within(screen.getByTestId('cell-alpha-1_model_ratio-acme')).getByTestId(
        'cbx',
      ),
    );
    fireEvent.click(screen.getByText('应用同步'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 1 });
  });
});
