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

For commercial licensing, please contact support@quantumnous.com
*/

// A tag edit rewrites EVERY channel carrying that tag in one PUT, so this
// file concentrates on handleSave/submit: what actually goes on the wire,
// which malformed inputs are refused before they get there, and whether a
// refusal from the server is reported as one.
//
// The Form stub exposes a submit button that calls onSubmit with a payload
// the test controls, so the save path can be driven precisely without
// simulating the (large) model/group picker UI.
//
// NOT COVERED HERE, and stated rather than faked: the custom-model adder, the
// group picker and the side sheet chrome. They need the full picker harness;
// mounting them just to assert "a div exists" would raise the number and
// catch nothing.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  submitPayload: { current: {} },
}));

vi.mock('react-i18next', () => {
  const t = (k) => k;
  const value = { t, i18n: { language: 'zh' } };
  return { useTranslation: () => value };
});

vi.mock('../../../../helpers', () => ({
  API: { get: H.get, put: H.put },
  showError: H.showError,
  showInfo: H.showInfo,
  showSuccess: H.showSuccess,
  showWarning: H.showWarning,
  // The real JSON validator — the point of these tests is that malformed
  // JSON is refused, so stubbing it to always-true would erase the contract.
  verifyJSON: (str) => {
    try {
      JSON.parse(str);
      return true;
    } catch (e) {
      return false;
    }
  },
  selectFilter: () => true,
  getChannelModels: () => [],
}));

vi.mock('@douyinfe/semi-icons', () => {
  const icon = (n) => () => React.createElement('i', { 'data-testid': n });
  return {
    IconSave: icon('i-save'),
    IconClose: icon('i-close'),
    IconBookmark: icon('i-bookmark'),
    IconUser: icon('i-user'),
    IconCode: icon('i-code'),
    IconSetting: icon('i-setting'),
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const nul = () => null;
  const box =
    (testid) =>
    ({ children }) =>
      React.createElement('div', { 'data-testid': testid }, children);

  const Form = ({ children, onSubmit, getFormApi }) => {
    React.useEffect(() => {
      getFormApi?.({
        setValue: () => {},
        setValues: () => {},
        // Returning {} models "the form api exists but has no values yet".
        getValues: () => ({}),
        reset: () => {},
      });
    }, []);
    return React.createElement(
      'div',
      { 'data-testid': 'form' },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'submit',
          onClick: () => onSubmit(H.submitPayload.current),
        },
        'save',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'submit-undefined',
          onClick: () => onSubmit(undefined),
        },
        'save-undefined',
      ),
      typeof children === 'function' ? null : children,
    );
  };
  for (const k of [
    'Input',
    'Select',
    'TextArea',
    'Switch',
    'Checkbox',
    'AutoComplete',
    'InputNumber',
    'Slot',
    'TagInput',
  ]) {
    Form[k] = nul;
  }

  return {
    SideSheet: ({ visible, children, footer }) =>
      visible
        ? React.createElement(
            'div',
            { 'data-testid': 'sheet' },
            children,
            footer,
          )
        : null,
    Form,
    Space: box('space'),
    Button: ({ children, onClick }) =>
      React.createElement('button', { type: 'button', onClick }, children),
    Typography: {
      Text: ({ children }) => React.createElement('span', null, children),
      Title: ({ children }) => React.createElement('span', null, children),
    },
    Spin: box('spin'),
    Banner: nul,
    Card: box('card'),
    Tag: ({ children }) => React.createElement('span', null, children),
    Avatar: nul,
  };
});

import EditTagModal from './EditTagModal';

const renderModal = (over = {}) => {
  const props = {
    visible: true,
    tag: 'eu-west',
    handleClose: vi.fn(),
    refresh: vi.fn(),
    ...over,
  };
  render(<EditTagModal {...props} />);
  return props;
};

const save = async (payload) => {
  H.submitPayload.current = payload;
  const user = userEvent.setup();
  await user.click(screen.getByTestId('submit'));
};

const putBody = () => H.put.mock.calls[0][1];

// How many times the component has told the user anything at all. Taken as a
// BEFORE/AFTER delta because the open-effect legitimately toasts on its own.
const toastCount = () =>
  H.showError.mock.calls.length +
  H.showWarning.mock.calls.length +
  H.showInfo.mock.calls.length +
  H.showSuccess.mock.calls.length;

// `/api/channel/tag/models` answers with a comma STRING; the other two answer
// with arrays. Getting this wrong makes the open-effect throw and fire a
// spurious toast, which would then mask what the save path did.
const defaultGet = (url) =>
  Promise.resolve({
    data: {
      success: true,
      data: String(url).includes('/api/channel/tag/models') ? '' : [],
    },
  });

beforeEach(() => {
  vi.clearAllMocks();
  H.get.mockImplementation(defaultGet);
  H.put.mockResolvedValue({ data: { success: true } });
  H.submitPayload.current = {};
});

describe('what goes on the wire', () => {
  it('always scopes the write to the tag being edited', async () => {
    renderModal({ tag: 'eu-west' });
    await save({ new_tag: 'eu-west-2' });
    await waitFor(() => expect(H.put).toHaveBeenCalledTimes(1));
    expect(H.put.mock.calls[0][0]).toBe('/api/channel/tag');
    // Losing `tag` would make the backend rewrite the wrong group, or all of
    // them.
    expect(putBody().tag).toBe('eu-west');
  });

  it('joins groups and models back into the comma strings the API expects', async () => {
    renderModal();
    await save({
      new_tag: 'eu-west',
      groups: ['default', 'vip'],
      models: ['gpt-4o', 'claude-3'],
    });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(putBody().groups).toBe('default,vip');
    expect(putBody().models).toBe('gpt-4o,claude-3');
  });

  it('omits groups and models entirely when they are empty', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', groups: [], models: [] });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    // An empty string here would blank the model list on every channel in
    // the group; omitting the field leaves it alone.
    expect(putBody()).not.toHaveProperty('groups');
    expect(putBody()).not.toHaveProperty('models');
  });

  it('trims the override payloads before sending them', async () => {
    renderModal();
    await save({
      new_tag: 'eu-west',
      param_override: '  {"temperature":0}  ',
      header_override: '\n{"x-a":"b"}\n',
    });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(putBody().param_override).toBe('{"temperature":0}');
    expect(putBody().header_override).toBe('{"x-a":"b"}');
  });

  it('sends an empty override through as an explicit clear', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', param_override: '   ' });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(putBody().param_override).toBe('');
  });
});

describe('malformed input is refused before the write', () => {
  it('refuses a model mapping that is not valid JSON', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', model_mapping: '{ not json' });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showInfo).toHaveBeenCalledWith('模型映射必须是合法的 JSON 格式！');
  });

  it('accepts a well-formed model mapping', async () => {
    renderModal();
    await save({
      new_tag: 'eu-west',
      model_mapping: '{"gpt-4o":"gpt-4o-mini"}',
    });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(putBody().model_mapping).toBe('{"gpt-4o":"gpt-4o-mini"}');
  });

  it('refuses a param override that is not valid JSON', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', param_override: '{bad' });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showInfo).toHaveBeenCalledWith('参数覆盖必须是合法的 JSON 格式！');
  });

  it('refuses a param override that is not even a string', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', param_override: { temperature: 0 } });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showInfo).toHaveBeenCalledWith('参数覆盖必须是合法的 JSON 格式！');
  });

  it('refuses a header override that is not valid JSON', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', header_override: '[' });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showInfo).toHaveBeenCalledWith(
      '请求头覆盖必须是合法的 JSON 格式！',
    );
  });

  it('refuses a header override that is not even a string', async () => {
    renderModal();
    await save({ new_tag: 'eu-west', header_override: 42 });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showInfo).toHaveBeenCalledWith(
      '请求头覆盖必须是合法的 JSON 格式！',
    );
  });
});

describe('outcome reporting', () => {
  it('confirms, refreshes and closes on a successful write', async () => {
    const props = renderModal();
    await save({ new_tag: 'eu-west-2' });
    await waitFor(() =>
      expect(H.showSuccess).toHaveBeenCalledWith('标签更新成功！'),
    );
    expect(props.refresh).toHaveBeenCalledTimes(1);
    expect(props.handleClose).toHaveBeenCalledTimes(1);
  });

  it('does not claim success, refresh or close when the request throws', async () => {
    const props = renderModal();
    H.put.mockRejectedValue(new Error('network down'));
    await save({ new_tag: 'eu-west-2' });
    await waitFor(() => expect(H.showError).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    expect(props.handleClose).not.toHaveBeenCalled();
  });

  // DEFECT (correctness): submit() only reacts to `res?.data?.success` being
  // truthy. A well-formed refusal — `{ success: false, message: '...' }`,
  // which is what this API returns for a missing tag, a permission failure or
  // a cross-tenant rejection — falls through every branch: no toast, no
  // refresh, no close, no console trace. The operator sees the side sheet sit
  // there exactly as if the click had not registered, and the natural next
  // move is to click Save again.
  //
  // Every other write path in this tree (MultiKeyManageModal, the action
  // rail's handlers, useChannelsData.batchDeleteChannels) does
  // `else showError(res.data.message)`. This one is the odd one out.
  //
  // Verified red 2026-08-20: un-skipped it fails, none of the three toast
  // spies is called.
  it.skip('CONTRACT:reports a server-side refusal instead of swallowing it', async () => {
    renderModal();
    H.put.mockResolvedValue({
      data: { success: false, message: 'tag not found' },
    });
    // Measure only what the SAVE said, not what opening the sheet said.
    await waitFor(() => expect(H.get).toHaveBeenCalled());
    const before = toastCount();
    await save({ new_tag: 'eu-west-2' });
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(toastCount()).toBeGreaterThan(before);
  });

  it('currently leaves a refused tag update entirely unreported', async () => {
    // Pinning the defect, not endorsing it — see the comment above. The
    // assertions are the ones that must hold under ANY fix: a refusal must
    // never be reported as success, and must not close the sheet as if the
    // change had landed.
    const props = renderModal();
    H.put.mockResolvedValue({
      data: { success: false, message: 'tag not found' },
    });
    await save({ new_tag: 'eu-west-2' });
    await waitFor(() => expect(H.put).toHaveBeenCalled());

    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    expect(props.handleClose).not.toHaveBeenCalled();
  });

  // DEFECT (correctness): the "没有任何修改！" guard tests
  // `data.new_tag === undefined`, but `data.new_tag = formVals.new_tag` is
  // assigned unconditionally two lines earlier, and the open-effect seeds the
  // form's new_tag to the CURRENT tag. So new_tag is always a string, the
  // guard can never fire through the UI, and pressing Save without changing
  // anything sends a no-op PUT and reports 标签更新成功！ — success for a
  // change that was never made. (The backend's `*newTag != tag` check is what
  // keeps this merely misleading rather than destructive.)
  //
  // Verified red 2026-08-20: un-skipped it fails, showWarning is not called
  // and the PUT goes out.
  it.skip('CONTRACT:refuses a save that carries no actual change', async () => {
    renderModal({ tag: 'eu-west' });
    await save({ tag: 'eu-west', new_tag: 'eu-west' });
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('没有任何修改！');
  });

  it('currently reports success for a save that changed nothing', async () => {
    // Pinning the defect, not endorsing it — see the comment above.
    renderModal({ tag: 'eu-west' });
    await save({ tag: 'eu-west', new_tag: 'eu-west' });
    await waitFor(() => expect(H.put).toHaveBeenCalledTimes(1));
    expect(putBody()).toEqual({ tag: 'eu-west', new_tag: 'eu-west' });
    expect(H.showWarning).not.toHaveBeenCalled();
  });

  it('does fire the no-change guard when the form yields no values at all', async () => {
    // The one path that still reaches the guard: onSubmit(undefined) with a
    // form api whose getValues() is empty. Proves the guard is live code,
    // just unreachable from the UI.
    const user = userEvent.setup();
    renderModal();
    await user.click(screen.getByTestId('submit-undefined'));
    expect(H.showWarning).toHaveBeenCalledWith('没有任何修改！');
    expect(H.put).not.toHaveBeenCalled();
  });
});

describe('opening the sheet', () => {
  it('loads the models already attached to the tag', async () => {
    H.get.mockImplementation((url) => {
      if (url.includes('/api/channel/tag/models')) {
        return Promise.resolve({
          data: { success: true, data: 'gpt-4o,claude-3' },
        });
      }
      return Promise.resolve({ data: { success: true, data: [] } });
    });
    renderModal({ tag: 'eu-west' });
    await waitFor(() =>
      expect(H.get).toHaveBeenCalledWith('/api/channel/tag/models?tag=eu-west'),
    );
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a refusal from the tag-models lookup', async () => {
    H.get.mockImplementation((url) => {
      if (url.includes('/api/channel/tag/models')) {
        return Promise.resolve({
          data: { success: false, message: 'no such tag' },
        });
      }
      return Promise.resolve({ data: { success: true, data: [] } });
    });
    renderModal({ tag: 'eu-west' });
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('no such tag'),
    );
  });

  it('renders nothing while closed', () => {
    renderModal({ visible: false });
    expect(screen.queryByTestId('sheet')).toBeNull();
  });
});
