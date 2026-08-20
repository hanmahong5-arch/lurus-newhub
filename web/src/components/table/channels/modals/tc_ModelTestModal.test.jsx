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

// The channel probe screen. Its job is to tell an operator, per model,
// whether the upstream answered — so the assertions here are about the
// per-model verdict being reported truthfully (a failed probe must never
// render as a success, and an untested model must never inherit a stale
// verdict from another channel) and about the batch button's count matching
// what the batch will actually do.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  copy: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../helpers', () => ({
  copy: H.copy,
  showError: H.showError,
  showInfo: H.showInfo,
  showSuccess: H.showSuccess,
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconSearch: () => React.createElement('i', { 'data-testid': 'icon-search' }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Modal = ({ visible, title, footer, children }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal', role: 'dialog' },
          React.createElement('div', { 'data-testid': 'modal-title' }, title),
          children,
          React.createElement('div', { 'data-testid': 'modal-footer' }, footer),
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

  const Input = ({ value, onChange, placeholder }) =>
    React.createElement('input', {
      'data-testid': 'model-search',
      value: value ?? '',
      placeholder,
      onChange: (e) => onChange(e.target.value),
    });

  const Table = ({ columns, dataSource, rowSelection, pagination }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'model-table',
        'data-total': String(pagination?.total),
        'data-page': String(pagination?.currentPage),
        'data-pagesize': String(pagination?.pageSize),
        'data-selected': (rowSelection?.selectedRowKeys || []).join('|'),
      },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'goto-page-2',
          onClick: () => pagination.onPageChange(2),
        },
        'page 2',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'select-all-on',
          onClick: () => rowSelection.onSelectAll(true),
        },
        'all',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'select-all-off',
          onClick: () => rowSelection.onSelectAll(false),
        },
        'none',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'select-one',
          onClick: () => rowSelection.onChange(['manual-pick']),
        },
        'one',
      ),
      (dataSource || []).map((record, rowIdx) =>
        React.createElement(
          'div',
          {
            key: record.key,
            'data-testid': 'model-row',
            'data-model': record.model,
          },
          columns.map((col, i) =>
            React.createElement(
              'span',
              { key: i, 'data-col': col.dataIndex },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
        ),
      ),
    );

  const Select = ({ value, onChange, optionList, placeholder }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'endpoint-select',
        'data-value': String(value),
        'data-options': JSON.stringify((optionList || []).map((o) => o.value)),
      },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'pick-anthropic',
          onClick: () => onChange('anthropic'),
        },
        placeholder,
      ),
    );

  return {
    Modal,
    Button,
    Input,
    Table,
    Select,
    Tag: ({ children, color }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tag', 'data-color': color },
        children,
      ),
    Typography: {
      Text: ({ children, type }) =>
        React.createElement('span', { 'data-type': type }, children),
    },
  };
});

import ModelTestModal from './ModelTestModal';

const t = (k) => k;

const CHANNEL = {
  id: 3,
  name: 'azure-prod',
  models: 'gpt-4o,gpt-4o-mini,claude-3',
};

const makeProps = (over = {}) => ({
  t,
  showModelTestModal: true,
  currentTestChannel: CHANNEL,
  handleCloseModal: vi.fn(),
  isBatchTesting: false,
  batchTestModels: vi.fn(),
  modelSearchKeyword: '',
  setModelSearchKeyword: vi.fn(),
  selectedModelKeys: [],
  setSelectedModelKeys: vi.fn(),
  modelTestResults: {},
  testingModels: new Set(),
  testChannel: vi.fn(),
  modelTablePage: 1,
  setModelTablePage: vi.fn(),
  selectedEndpointType: '',
  setSelectedEndpointType: vi.fn(),
  allSelectingRef: { current: false },
  isMobile: false,
  ...over,
});

const renderModal = (over = {}) => {
  const props = makeProps(over);
  render(<ModelTestModal {...props} />);
  return props;
};

const rowFor = (model) =>
  screen.getAllByTestId('model-row').find((n) => n.dataset.model === model);

beforeEach(() => {
  vi.clearAllMocks();
  H.copy.mockResolvedValue(true);
});

describe('closed / empty states', () => {
  it('renders nothing when the modal is hidden', () => {
    renderModal({ showModelTestModal: false });
    expect(screen.queryByTestId('modal')).toBeNull();
  });

  it('renders the shell but no table when there is no channel to probe', () => {
    renderModal({ currentTestChannel: null });
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.queryByTestId('model-table')).toBeNull();
    expect(screen.getByTestId('modal-footer')).toBeEmptyDOMElement();
  });
});

describe('model list and filtering', () => {
  it('lists every model on the channel and names it in the header', () => {
    renderModal();
    expect(
      screen.getAllByTestId('model-row').map((n) => n.dataset.model),
    ).toEqual(['gpt-4o', 'gpt-4o-mini', 'claude-3']);
    const title = screen.getByTestId('modal-title').textContent;
    expect(title).toContain('azure-prod');
    expect(title).toContain('3');
  });

  it('filters case-insensitively on the search keyword', () => {
    renderModal({ modelSearchKeyword: 'GPT' });
    expect(
      screen.getAllByTestId('model-row').map((n) => n.dataset.model),
    ).toEqual(['gpt-4o', 'gpt-4o-mini']);
    expect(screen.getByTestId('model-table').dataset.total).toBe('2');
  });

  it('shows no rows for a keyword that matches nothing', () => {
    renderModal({ modelSearchKeyword: 'llama' });
    expect(screen.queryAllByTestId('model-row')).toHaveLength(0);
    expect(screen.getByTestId('model-table').dataset.total).toBe('0');
  });

  it('resets to page 1 when the keyword changes', async () => {
    const user = userEvent.setup();
    const props = renderModal({ modelTablePage: 3 });
    await user.type(screen.getByTestId('model-search'), 'g');
    expect(props.setModelSearchKeyword).toHaveBeenCalledWith('g');
    // Leaving the operator on page 3 of a now one-page result set shows an
    // empty table.
    expect(props.setModelTablePage).toHaveBeenCalledWith(1);
  });

  it('pages the list ten at a time', () => {
    const models = Array.from({ length: 23 }, (_, i) => `m${i}`).join(',');
    renderModal({
      currentTestChannel: { ...CHANNEL, models },
      modelTablePage: 3,
    });
    expect(screen.getByTestId('model-table').dataset.pagesize).toBe('10');
    expect(screen.getByTestId('model-table').dataset.total).toBe('23');
    // Page 3 of 23 is the last three.
    expect(
      screen.getAllByTestId('model-row').map((n) => n.dataset.model),
    ).toEqual(['m20', 'm21', 'm22']);
  });

  it('changes page on demand', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByTestId('goto-page-2'));
    expect(props.setModelTablePage).toHaveBeenCalledWith(2);
  });
});

describe('per-model verdicts', () => {
  it('shows an untested model as 未开始, with no time and no colour claim', () => {
    renderModal();
    const row = rowFor('gpt-4o');
    expect(row.textContent).toContain('未开始');
    expect(row.querySelector('[data-testid="tag"]').dataset.color).toBe('grey');
  });

  it('shows a passing probe as 成功 with its duration', () => {
    renderModal({
      modelTestResults: { '3-gpt-4o': { success: true, time: 1.2345 } },
    });
    const row = rowFor('gpt-4o');
    expect(row.querySelector('[data-testid="tag"]').dataset.color).toBe(
      'green',
    );
    expect(row.textContent).toContain('成功');
    expect(row.textContent).toContain('1.23');
  });

  it('shows a failing probe as 失败 and prints NO duration next to it', () => {
    renderModal({
      modelTestResults: { '3-gpt-4o': { success: false, time: 9.9 } },
    });
    const row = rowFor('gpt-4o');
    expect(row.querySelector('[data-testid="tag"]').dataset.color).toBe('red');
    expect(row.textContent).toContain('失败');
    // A duration beside a failure reads as "it answered in 9.9s" — it did not.
    expect(row.textContent).not.toContain('9.90');
    expect(row.textContent).not.toContain('请求时长');
  });

  it('shows an in-flight probe as 测试中 even when a stale result exists', () => {
    renderModal({
      modelTestResults: { '3-gpt-4o': { success: true, time: 1 } },
      testingModels: new Set(['gpt-4o']),
    });
    const row = rowFor('gpt-4o');
    expect(row.textContent).toContain('测试中');
    expect(row.textContent).not.toContain('成功');
  });

  it("never reads another channel's verdict for the same model name", () => {
    // Results are keyed `${channelId}-${model}`. If that key ever loses the
    // channel id, this row would light up green off channel 99's result.
    renderModal({
      modelTestResults: { '99-gpt-4o': { success: true, time: 1 } },
    });
    expect(rowFor('gpt-4o').textContent).toContain('未开始');
  });

  it('probes a single model against the selected endpoint type', async () => {
    const user = userEvent.setup();
    const props = renderModal({ selectedEndpointType: 'anthropic' });
    await user.click(screen.getAllByText('测试')[0]);
    expect(props.testChannel).toHaveBeenCalledWith(
      CHANNEL,
      'gpt-4o',
      'anthropic',
    );
  });
});

describe('endpoint type selector', () => {
  it('offers auto-detect plus the documented endpoint families', () => {
    renderModal();
    const opts = JSON.parse(
      screen.getByTestId('endpoint-select').dataset.options,
    );
    expect(opts[0]).toBe('');
    expect(opts).toEqual(
      expect.arrayContaining([
        'openai',
        'openai-response',
        'anthropic',
        'gemini',
        'jina-rerank',
        'image-generation',
        'embeddings',
      ]),
    );
  });

  it('hands the chosen endpoint type straight back up', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByTestId('pick-anthropic'));
    expect(props.setSelectedEndpointType).toHaveBeenCalledWith('anthropic');
  });
});

describe('selection helpers', () => {
  it('refuses to copy an empty selection instead of copying an empty string', async () => {
    const user = userEvent.setup();
    renderModal({ selectedModelKeys: [] });
    await user.click(screen.getByText('复制已选'));
    expect(H.copy).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalledWith('请先选择模型！');
  });

  it('copies the selection as a comma list and confirms the count', async () => {
    const user = userEvent.setup();
    renderModal({ selectedModelKeys: ['gpt-4o', 'claude-3'] });
    await user.click(screen.getByText('复制已选'));
    expect(H.copy).toHaveBeenCalledWith('gpt-4o,claude-3');
    await waitFor(() =>
      expect(H.showSuccess).toHaveBeenCalledWith('已复制 2 个模型'),
    );
  });

  it('reports a refused clipboard write rather than claiming a copy happened', async () => {
    const user = userEvent.setup();
    H.copy.mockResolvedValue(false);
    renderModal({ selectedModelKeys: ['gpt-4o'] });
    await user.click(screen.getByText('复制已选'));
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('复制失败，请手动复制'),
    );
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('selects exactly the models that passed, and nothing that failed', async () => {
    const user = userEvent.setup();
    const props = renderModal({
      modelTestResults: {
        '3-gpt-4o': { success: true, time: 1 },
        '3-gpt-4o-mini': { success: false, time: 0 },
        '3-claude-3': { success: true, time: 2 },
      },
    });
    await user.click(screen.getByText('选择成功'));
    expect(props.setSelectedModelKeys).toHaveBeenCalledWith([
      'gpt-4o',
      'claude-3',
    ]);
    expect(H.showInfo).not.toHaveBeenCalled();
  });

  it('honours the active keyword when selecting the passing models', async () => {
    const user = userEvent.setup();
    const props = renderModal({
      modelSearchKeyword: 'claude',
      modelTestResults: {
        '3-gpt-4o': { success: true, time: 1 },
        '3-claude-3': { success: true, time: 2 },
      },
    });
    await user.click(screen.getByText('选择成功'));
    expect(props.setSelectedModelKeys).toHaveBeenCalledWith(['claude-3']);
  });

  it('says so when nothing passed rather than silently selecting nothing', async () => {
    const user = userEvent.setup();
    renderModal({
      modelTestResults: { '3-gpt-4o': { success: false, time: 0 } },
    });
    await user.click(screen.getByText('选择成功'));
    expect(H.showInfo).toHaveBeenCalledWith('暂无成功模型');
  });

  it('select-all selects every FILTERED model, not just the visible page', async () => {
    const user = userEvent.setup();
    const models = Array.from({ length: 23 }, (_, i) => `m${i}`).join(',');
    const props = renderModal({
      currentTestChannel: { ...CHANNEL, models },
    });
    await user.click(screen.getByTestId('select-all-on'));
    expect(props.setSelectedModelKeys.mock.calls[0][0]).toHaveLength(23);
    expect(props.allSelectingRef.current).toBe(true);
  });

  it('select-all-off clears the selection entirely', async () => {
    const user = userEvent.setup();
    const props = renderModal({ selectedModelKeys: ['gpt-4o'] });
    await user.click(screen.getByTestId('select-all-off'));
    expect(props.setSelectedModelKeys).toHaveBeenCalledWith([]);
  });

  it('swallows the row-change echo that immediately follows a select-all', async () => {
    const user = userEvent.setup();
    const props = renderModal({ allSelectingRef: { current: true } });
    await user.click(screen.getByTestId('select-one'));
    // Without this guard Semi's own onChange fires right after onSelectAll
    // and narrows the just-made full selection back down to one page.
    expect(props.setSelectedModelKeys).not.toHaveBeenCalled();
    expect(props.allSelectingRef.current).toBe(false);
  });

  it('accepts an ordinary row change when no select-all is in flight', async () => {
    const user = userEvent.setup();
    const props = renderModal({ allSelectingRef: { current: false } });
    await user.click(screen.getByTestId('select-one'));
    expect(props.setSelectedModelKeys).toHaveBeenCalledWith(['manual-pick']);
  });
});

describe('batch testing footer', () => {
  it('counts the FILTERED models on the batch button, matching what it will run', () => {
    renderModal({ modelSearchKeyword: 'gpt' });
    // useChannelsData.batchTestModels applies the same keyword filter, so an
    // unfiltered count here would overstate the work by the filtered-out
    // models and mislead on cost.
    expect(screen.getByText('批量测试2个模型')).toBeInTheDocument();
  });

  it('starts the batch on demand', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByText('批量测试3个模型'));
    expect(props.batchTestModels).toHaveBeenCalledTimes(1);
  });

  it('offers a stop button and blocks a second batch while one is running', () => {
    renderModal({ isBatchTesting: true });
    expect(screen.getByText('停止测试')).toBeInTheDocument();
    expect(screen.queryByText('取消')).toBeNull();
    expect(screen.getByText('测试中...')).toBeDisabled();
  });

  it('offers a plain cancel when idle', () => {
    renderModal({ isBatchTesting: false });
    expect(screen.getByText('取消')).toBeInTheDocument();
    expect(screen.queryByText('停止测试')).toBeNull();
  });

  it('closes through the same handler whether idle or mid-batch', async () => {
    const user = userEvent.setup();
    const props = renderModal({ isBatchTesting: true });
    await user.click(screen.getByText('停止测试'));
    expect(props.handleCloseModal).toHaveBeenCalledTimes(1);
  });
});
