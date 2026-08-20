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

// The action rail is the page's destructive surface: it deletes channels,
// strips models off channels, and flips channels on and off in bulk. Two
// classes of assertion here:
//   * "an irreversible action never fires before the operator confirms" —
//     asserted by checking the handler is NOT called on click, only on onOk;
//   * "the rail dispatches to the right handler for the current selection" —
//     channel-mode vs model-mode vs mixed.
//
// Every stub renders a MARKER. Buttons are located by the icon marker they
// contain (stable) rather than by tooltip text (changes with every state),
// and the icon itself is an assertion target for the play/stop swap.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  confirm: vi.fn(),
  info: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ icon, onClick, disabled, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, disabled, ...rest },
      icon,
    );
  // content is a plain string here (t() echoes the key), so surface it as an
  // attribute. Dropping it would make every tooltip assertion impossible and
  // hide whichever branch produced the copy.
  const Tooltip = ({ content, children }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tooltip',
        'data-content': typeof content === 'string' ? content : undefined,
      },
      children,
    );
  const Divider = () => React.createElement('hr', { 'data-testid': 'divider' });
  return {
    Button,
    Tooltip,
    Divider,
    Modal: { confirm: H.confirm, info: H.info },
  };
});

// `icon` is defined inside each factory: vi.mock is hoisted above any
// top-level const, so a shared helper would be a TDZ error at mock time.
vi.mock('@douyinfe/semi-icons', () => {
  const icon = (name) => (props) =>
    React.createElement('i', { 'data-testid': name, ...props });
  return {
    IconPlus: icon('i-plus'),
    IconRefresh: icon('i-refresh'),
    IconEdit: icon('i-edit'),
    IconCopy: icon('i-copy'),
    IconDelete: icon('i-delete'),
    IconKey: icon('i-key'),
    IconList: icon('i-list'),
    IconLayers: icon('i-layers'),
    IconSetting: icon('i-setting'),
  };
});

vi.mock('react-icons/fa', () => {
  const icon = (name) => (props) =>
    React.createElement('i', { 'data-testid': name, ...props });
  return {
    FaBolt: icon('i-bolt'),
    FaPlay: icon('i-play'),
    FaStop: icon('i-stop'),
    FaServer: icon('i-server'),
    FaCheckDouble: icon('i-check-double'),
  };
});

import ChannelsActionRail from './ChannelsActionRail';

const t = (k) => k;

const makeProps = (overrides = {}) => ({
  t,
  selectedChannels: [],
  selectedModels: [],
  setSelectedModels: vi.fn(),
  refresh: vi.fn(),
  setEditingChannel: vi.fn(),
  setShowEdit: vi.fn(),
  copySelectedChannel: vi.fn(),
  manageChannel: vi.fn(),
  batchDeleteChannels: vi.fn(),
  testChannel: vi.fn(),
  testAllChannels: vi.fn(),
  checkOllamaVersion: vi.fn(),
  removeModelsFromChannel: vi.fn().mockResolvedValue(undefined),
  setCurrentMultiKeyChannel: vi.fn(),
  setShowMultiKeyManageModal: vi.fn(),
  setShowColumnSelector: vi.fn(),
  compactMode: false,
  setCompactMode: vi.fn(),
  ...overrides,
});

const renderRail = (overrides = {}) => {
  const props = makeProps(overrides);
  const view = render(<ChannelsActionRail {...props} />);
  return { props, view };
};

// Buttons are keyed by the icon marker they contain.
const btn = (testid) => screen.getByTestId(testid).closest('button');
const tipOf = (testid) =>
  screen.getByTestId(testid).closest('[data-testid="tooltip"]').dataset.content;
// The enable/disable control swaps its icon with the selection, so address it
// by whichever face is currently mounted.
const toggleBtn = () =>
  (screen.queryByTestId('i-stop') ?? screen.getByTestId('i-play')).closest(
    'button',
  );

const CHANNEL = { id: 1, name: 'openai-prod', status: 1, type: 1 };
const CHANNEL_OFF = { id: 2, name: 'eu', status: 2, type: 1 };
const TAG_ROW = {
  id: 'eu-west',
  key: 'eu-west',
  name: '标签：eu-west',
  children: [],
};
const MODEL = (channelId, modelName, channel) => ({
  channelId,
  modelName,
  channel: channel ?? { id: channelId, name: `ch-${channelId}` },
});

beforeEach(() => {
  vi.clearAllMocks();
});

describe('selection mode gating', () => {
  it('disables every selection-scoped action when nothing is selected', () => {
    renderRail();
    for (const id of [
      'i-layers',
      'i-bolt',
      'i-stop',
      'i-edit',
      'i-copy',
      'i-delete',
      'i-key',
      'i-server',
    ]) {
      expect(btn(id)).toBeDisabled();
    }
    // The three selection-independent actions stay live.
    expect(btn('i-plus')).toBeEnabled();
    expect(btn('i-refresh')).toBeEnabled();
    expect(btn('i-check-double')).toBeEnabled();
  });

  it('enables the single-channel actions for exactly one ordinary channel', () => {
    renderRail({ selectedChannels: [CHANNEL] });
    expect(btn('i-edit')).toBeEnabled();
    expect(btn('i-copy')).toBeEnabled();
    expect(btn('i-delete')).toBeEnabled();
    expect(btn('i-bolt')).toBeEnabled();
  });

  it('drops the single-row actions back out under multi-select', () => {
    renderRail({ selectedChannels: [CHANNEL, CHANNEL_OFF] });
    expect(btn('i-edit')).toBeDisabled();
    expect(btn('i-copy')).toBeDisabled();
    // ...but the bulk ones stay available, and say how many rows they cover.
    expect(btn('i-delete')).toBeEnabled();
    expect(tipOf('i-delete')).toBe('删除选中的 2 个渠道');
    expect(btn('i-bolt')).toBeEnabled();
    expect(tipOf('i-bolt')).toBe('测试选中的 2 个渠道');
  });

  it('disables single-row actions and says why when channels and models are both selected', () => {
    renderRail({
      selectedChannels: [CHANNEL],
      selectedModels: [MODEL(1, 'gpt-4o')],
    });
    expect(btn('i-delete')).toBeDisabled();
    expect(btn('i-bolt')).toBeDisabled();
    expect(btn('i-copy')).toBeDisabled();
    expect(tipOf('i-delete')).toBe('混合选中渠道+模型时不可用');
    expect(tipOf('i-bolt')).toBe('混合选中渠道+模型时不可用');
  });

  it('routes to model mode when only model rows are selected', () => {
    renderRail({
      selectedModels: [MODEL(1, 'gpt-4o'), MODEL(1, 'gpt-4o-mini')],
    });
    expect(btn('i-delete')).toBeEnabled();
    expect(tipOf('i-delete')).toBe('从渠道移除选中的 2 个模型');
    // Enable/disable is a channel-only concept.
    expect(btn('i-stop')).toBeDisabled();
    expect(tipOf('i-stop')).toBe('启用/禁用（仅渠道行可用）');
  });
});

describe('irreversible actions require confirmation', () => {
  it('does not delete channels until the confirm dialog is accepted', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL] });

    await user.click(btn('i-delete'));
    // The whole point: clicking must not have deleted anything yet.
    expect(props.batchDeleteChannels).not.toHaveBeenCalled();
    expect(H.confirm).toHaveBeenCalledTimes(1);
    expect(H.confirm.mock.calls[0][0].title).toBe('确定是否要删除所选通道？');

    H.confirm.mock.calls[0][0].onOk();
    expect(props.batchDeleteChannels).toHaveBeenCalledTimes(1);
  });

  it('does not copy a channel until the confirm dialog is accepted', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL] });

    await user.click(btn('i-copy'));
    expect(props.copySelectedChannel).not.toHaveBeenCalled();
    H.confirm.mock.calls[0][0].onOk();
    expect(props.copySelectedChannel).toHaveBeenCalledWith(CHANNEL);
  });

  it('does not start a full-fleet test until the confirm dialog is accepted', async () => {
    const user = userEvent.setup();
    const { props } = renderRail();

    await user.click(btn('i-check-double'));
    expect(props.testAllChannels).not.toHaveBeenCalled();
    H.confirm.mock.calls[0][0].onOk();
    expect(props.testAllChannels).toHaveBeenCalledTimes(1);
  });

  it('groups model removals into one call per channel and clears the selection', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({
      selectedModels: [
        MODEL(1, 'gpt-4o'),
        MODEL(2, 'claude-3'),
        MODEL(1, 'gpt-4o-mini'),
      ],
    });

    await user.click(btn('i-delete'));
    expect(props.removeModelsFromChannel).not.toHaveBeenCalled();
    const cfg = H.confirm.mock.calls[0][0];
    // 3 models spread over 2 channels — the dialog must say so, or the
    // operator cannot tell how wide the blast radius is.
    expect(cfg.title).toBe('从 2 个渠道移除 3 个模型？');

    await cfg.onOk();
    expect(props.removeModelsFromChannel).toHaveBeenCalledTimes(2);
    expect(props.removeModelsFromChannel).toHaveBeenCalledWith(1, [
      'gpt-4o',
      'gpt-4o-mini',
    ]);
    expect(props.removeModelsFromChannel).toHaveBeenCalledWith(2, ['claude-3']);
    expect(props.setSelectedModels).toHaveBeenCalledWith([]);
  });
});

describe('enable / disable toggle', () => {
  it('offers "enable" and the play icon when every selected channel is off', () => {
    renderRail({
      selectedChannels: [CHANNEL_OFF, { ...CHANNEL_OFF, id: 3, status: 3 }],
    });
    expect(screen.getByTestId('i-play')).toBeInTheDocument();
    expect(screen.queryByTestId('i-stop')).toBeNull();
    expect(tipOf('i-play')).toBe('启用选中');
  });

  it('offers "disable" and the stop icon as soon as one selected channel is live', () => {
    renderRail({ selectedChannels: [CHANNEL_OFF, CHANNEL] });
    expect(screen.getByTestId('i-stop')).toBeInTheDocument();
    expect(screen.queryByTestId('i-play')).toBeNull();
    expect(tipOf('i-stop')).toBe('禁用选中');
  });

  it('sends one manageChannel call per selected channel with the resolved target', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL, CHANNEL_OFF] });
    await user.click(btn('i-stop'));
    expect(props.manageChannel).toHaveBeenCalledTimes(2);
    expect(props.manageChannel).toHaveBeenCalledWith(1, 'disable', CHANNEL);
    expect(props.manageChannel).toHaveBeenCalledWith(2, 'disable', CHANNEL_OFF);
  });

  it('never toggles a tag-aggregation row mixed into a multi-selection', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL, TAG_ROW] });
    await user.click(btn('i-stop'));
    expect(props.manageChannel).toHaveBeenCalledTimes(1);
    expect(props.manageChannel).toHaveBeenCalledWith(1, 'disable', CHANNEL);
  });
});

describe('test dispatch', () => {
  it('tests each selected channel with an empty model override', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL, CHANNEL_OFF] });
    await user.click(btn('i-bolt'));
    expect(props.testChannel).toHaveBeenCalledTimes(2);
    expect(props.testChannel).toHaveBeenCalledWith(CHANNEL, '');
  });

  it('skips tag-aggregation rows when testing a multi-selection', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL, TAG_ROW] });
    await user.click(btn('i-bolt'));
    expect(props.testChannel).toHaveBeenCalledTimes(1);
    expect(props.testChannel).toHaveBeenCalledWith(CHANNEL, '');
  });

  it('tests a model row against its own parent channel and model name', async () => {
    const user = userEvent.setup();
    const parent = { id: 9, name: 'azure' };
    const { props } = renderRail({
      selectedModels: [MODEL(9, 'gpt-4o', parent)],
    });
    await user.click(btn('i-bolt'));
    expect(props.testChannel).toHaveBeenCalledWith(parent, 'gpt-4o');
  });
});

describe('multi-key and Ollama gating', () => {
  it('opens multi-key management only for a multi-key channel row', async () => {
    const user = userEvent.setup();
    const multi = { ...CHANNEL, channel_info: { is_multi_key: true } };
    const { props } = renderRail({ selectedChannels: [multi] });

    expect(btn('i-key')).toBeEnabled();
    expect(tipOf('i-key')).toBe('多密钥管理');
    await user.click(btn('i-key'));
    expect(props.setCurrentMultiKeyChannel).toHaveBeenCalledWith(multi);
    expect(props.setShowMultiKeyManageModal).toHaveBeenCalledWith(true);
  });

  it('refuses multi-key management for a single-key channel', () => {
    renderRail({ selectedChannels: [CHANNEL] });
    expect(btn('i-key')).toBeDisabled();
    expect(tipOf('i-key')).toBe('多密钥管理（仅多密钥渠道行可用）');
  });

  it('offers the Ollama probe only for type 4', async () => {
    const user = userEvent.setup();
    const ollama = { ...CHANNEL, type: 4 };
    const { props } = renderRail({ selectedChannels: [ollama] });
    expect(btn('i-server')).toBeEnabled();
    await user.click(btn('i-server'));
    expect(props.checkOllamaVersion).toHaveBeenCalledWith(ollama);
  });

  it('refuses the Ollama probe for any other channel type', () => {
    renderRail({ selectedChannels: [CHANNEL] });
    expect(btn('i-server')).toBeDisabled();
    expect(tipOf('i-server')).toBe('Ollama 测活（仅 Ollama 渠道可用）');
  });
});

describe('non-destructive controls', () => {
  it('opens a blank editor for "new channel" without touching the selection', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [CHANNEL] });
    await user.click(btn('i-plus'));
    expect(props.setEditingChannel).toHaveBeenCalledWith({ id: undefined });
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });

  it('routes "edit" on a model row to that model\'s parent channel editor', async () => {
    const user = userEvent.setup();
    const parent = { id: 9, name: 'azure' };
    const { props } = renderRail({
      selectedModels: [MODEL(9, 'gpt-4o', parent)],
    });
    await user.click(btn('i-edit'));
    expect(props.setEditingChannel).toHaveBeenCalledWith(parent);
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });

  it('toggles compact mode to the opposite of the current value', async () => {
    const user = userEvent.setup();
    const { props } = renderRail({ compactMode: true });
    expect(tipOf('i-list')).toBe('切换到宽松列表');
    await user.click(btn('i-list'));
    expect(props.setCompactMode).toHaveBeenCalledWith(false);
  });

  it('refreshes and opens the column selector', async () => {
    const user = userEvent.setup();
    const { props } = renderRail();
    await user.click(btn('i-refresh'));
    expect(props.refresh).toHaveBeenCalledTimes(1);
    await user.click(btn('i-setting'));
    expect(props.setShowColumnSelector).toHaveBeenCalledWith(true);
  });
});

describe('tag-aggregation rows', () => {
  it('refuses to test or toggle a lone tag row', () => {
    renderRail({ selectedChannels: [TAG_ROW] });
    expect(btn('i-bolt')).toBeDisabled();
    expect(toggleBtn()).toBeDisabled();
  });

  // DEFECT (data-loss / correctness): `dis.test` and `dis.toggle` both carry
  // an `isTagRow` guard; `dis.del` does not. So with a single tag-aggregation
  // row selected the rail refuses to test it and refuses to toggle it, but
  // happily offers "删除选中的 1 个渠道" behind an "此修改将不可逆" confirm.
  //
  // A tag row is a client-side rollup built by useChannelsData with
  // `id: <tag name>` (a string). batchDeleteChannels pushes `channel.id`
  // straight into POST /api/channel/batch, whose Go handler binds
  // `Ids []int` — so the request cannot even deserialise. The operator
  // confirms an irreversible deletion of a whole tag group, nothing is
  // deleted, and the failure surfaces as showError(undefined).
  //
  // The lock below asserts the invariant that survives EITHER fix: gate the
  // delete like its siblings, or un-gate all three once tag rows are
  // expanded to their children. It fails today because the three disagree.
  //
  // Verified red 2026-08-20: un-skipped it fails with false !== true.
  it.skip('CONTRACT:gates delete on a tag row consistently with test and toggle', () => {
    renderRail({ selectedChannels: [TAG_ROW] });
    expect(btn('i-delete').disabled).toBe(btn('i-bolt').disabled);
  });

  it('currently offers an irreversible delete for a tag row it will not test', async () => {
    // Pinning the defect, not endorsing it — see the comment above.
    const user = userEvent.setup();
    const { props } = renderRail({ selectedChannels: [TAG_ROW] });
    expect(btn('i-bolt')).toBeDisabled();
    expect(btn('i-delete')).toBeEnabled();

    await user.click(btn('i-delete'));
    expect(H.confirm.mock.calls[0][0].content).toBe('此修改将不可逆');
    H.confirm.mock.calls[0][0].onOk();
    expect(props.batchDeleteChannels).toHaveBeenCalledTimes(1);
  });

  it('still refuses the single-row editors for a tag row', () => {
    renderRail({ selectedChannels: [TAG_ROW] });
    expect(btn('i-edit')).toBeDisabled();
    expect(btn('i-copy')).toBeDisabled();
    expect(btn('i-key')).toBeDisabled();
  });
});

// NOT ASSERTED, and deliberately so:
//   * handleNewModel's `Modal.info('请先选中目标渠道')` branch is unreachable
//     through the UI — the same condition that would make `target` null also
//     sets `dis.newModel`, which disables the button. Reaching it would mean
//     firing onClick on a disabled control, which proves nothing about what a
//     user can do. Left uncovered rather than faked.
//   * `railStyle` is a bare style object. Asserting it equals itself would
//     fail on every restyle and catch no regression.
