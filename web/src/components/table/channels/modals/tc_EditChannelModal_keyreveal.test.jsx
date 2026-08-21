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

// EditChannelModal is ~3 300 lines and almost all of it is form JSX. This
// file deliberately covers ONE narrow slice: the step-up-verified "reveal the
// upstream provider key" path. That slice is the only place in the channels
// tree where a live provider credential is handled in the browser, so it is
// worth testing on its own even though the surrounding form is not.
//
// The component is mounted with the side sheet closed. The key-display modal
// lives OUTSIDE the sheet, so the reveal path is fully reachable while the
// heavy form body stays unrendered.
//
// NOT COVERED HERE, and stated rather than faked: the channel form itself
// (type presets, model mapping, region/key-format branches, submit
// validation). Those need a different, much larger harness; pretending
// otherwise by mounting the sheet and asserting "it rendered" would add
// coverage and no safety.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, fireEvent } from '@testing-library/react';

const SECRET = 'sk-live-51H8vQrTOPSECRET0000';

const H = vi.hoisted(() => ({
  onSuccess: { current: null },
  withVerification: vi.fn(),
  viewChannelKey: vi.fn((id) => ({ kind: 'viewChannelKey', id })),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiPut: vi.fn(),
}));

// `t` MUST be referentially stable: the component has an effect with `t` in
// its dependency list that calls setModelOptions, so a fresh t() per render
// (what a naive mock returns) spins that effect forever. Real react-i18next
// memoises t, so this mirrors production rather than papering over anything.
vi.mock('react-i18next', () => {
  const t = (k) => k;
  const i18n = { language: 'zh' };
  const value = { t, i18n };
  return { useTranslation: () => value };
});

vi.mock('../../../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => false,
  MOBILE_BREAKPOINT: 768,
}));

// Capture the onSuccess callback the component hands the verification hook.
// That callback IS the code under test.
vi.mock('../../../../hooks/common/useSecureVerification', () => ({
  useSecureVerification: ({ onSuccess }) => {
    H.onSuccess.current = onSuccess;
    return {
      isModalVisible: false,
      verificationMethods: [],
      verificationState: { title: '', description: '' },
      withVerification: H.withVerification,
      executeVerification: vi.fn(),
      cancelVerification: vi.fn(),
      setVerificationCode: vi.fn(),
      switchVerificationMethod: vi.fn(),
    };
  },
}));

vi.mock('../../../../services/secureVerification', () => ({
  createApiCalls: { viewChannelKey: H.viewChannelKey },
}));

vi.mock('../../../../helpers', () => ({
  API: {
    get: H.apiGet,
    post: H.apiPost,
    put: H.apiPut,
  },
  showError: H.showError,
  showInfo: H.showInfo,
  showSuccess: H.showSuccess,
  verifyJSON: () => true,
  getChannelModels: () => [],
  copy: vi.fn().mockResolvedValue(true),
  getChannelIcon: () => React.createElement('i', null),
  getModelCategories: () => ({ all: { filter: () => true } }),
  selectFilter: () => true,
}));

// The key display is the legitimate destination for the revealed key, so it
// must render a MARKER carrying what it was given — asserting on "a modal
// appeared" would not distinguish showing the key from showing nothing.
vi.mock('../../../common/ui/ChannelKeyDisplay', () => ({
  default: ({ keyData }) =>
    React.createElement('div', {
      'data-testid': 'key-display',
      'data-key': String(keyData ?? ''),
    }),
}));

vi.mock('../../../common/modals/SecureVerificationModal', () => ({
  default: ({ visible }) =>
    visible
      ? React.createElement('div', { 'data-testid': 'verify-modal' })
      : null,
}));

vi.mock('../../../common/ui/JSONEditor', () => ({
  default: () => React.createElement('div', { 'data-testid': 'json-editor' }),
}));

vi.mock('./ModelSelectModal', () => ({
  default: () => null,
}));

vi.mock('./OllamaModelModal', () => ({
  default: () => null,
}));

vi.mock('@douyinfe/semi-icons', () => {
  const icon = (name) => () =>
    React.createElement('i', { 'data-testid': name });
  return {
    IconSave: icon('i-save'),
    IconClose: icon('i-close'),
    IconServer: icon('i-server'),
    IconSetting: icon('i-setting'),
    IconCode: icon('i-code'),
    IconGlobe: icon('i-globe'),
    IconBolt: icon('i-bolt'),
    IconChevronUp: icon('i-up'),
    IconChevronDown: icon('i-down'),
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const nul = () => null;
  const box =
    (testid) =>
    ({ children }) =>
      React.createElement('div', { 'data-testid': testid }, children);

  const Modal = ({ visible, title, children, footer }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'key-modal', role: 'dialog' },
          React.createElement('div', null, title),
          children,
          footer,
        )
      : null;
  Modal.confirm = vi.fn();
  Modal.warning = vi.fn();
  Modal.error = vi.fn();
  Modal.info = vi.fn();

  const Form = ({ children, getFormApi }) => {
    React.useEffect(() => {
      getFormApi?.({
        setValue: () => {},
        setValues: () => {},
        getValues: () => ({}),
        reset: () => {},
        getFormState: () => ({ values: {} }),
      });
    }, []);
    // Semi's Form takes a render prop. Returning null for it would leave the
    // whole form body — including the "查看密钥" button that drives the
    // already-verified reveal path — permanently unmounted, so that path could
    // be deleted outright without a single test noticing. Invoke it instead.
    return React.createElement(
      'form',
      null,
      typeof children === 'function' ? children({}) : children,
    );
  };
  // These MUST NOT be `() => null`. The "查看密钥" button that drives the
  // already-verified reveal path is passed as the `extraText` PROP of a
  // Form.Input — a null stub swallows it, leaving that whole code path
  // unmountable and therefore unassertable. Render the slotted nodes.
  const field =
    (name) =>
    ({ extraText, label, children }) =>
      React.createElement(
        'div',
        { 'data-testid': `form-${name}` },
        label,
        extraText,
        children,
      );
  for (const k of [
    'Input',
    'Select',
    'TextArea',
    'Switch',
    'Checkbox',
    'AutoComplete',
    'InputNumber',
    'Slot',
    'RadioGroup',
    'Radio',
    'TagInput',
  ]) {
    Form[k] = field(k);
  }

  return {
    SideSheet: ({ visible, children }) =>
      visible ? React.createElement('div', null, children) : null,
    Modal,
    Form,
    Button: ({ children, onClick }) =>
      React.createElement('button', { type: 'button', onClick }, children),
    Space: box('space'),
    Spin: box('spin'),
    Typography: {
      Text: ({ children }) => React.createElement('span', null, children),
      Title: ({ children }) => React.createElement('span', null, children),
      Paragraph: ({ children }) => React.createElement('span', null, children),
    },
    Checkbox: nul,
    Banner: nul,
    ImagePreview: nul,
    Card: box('card'),
    Tag: ({ children }) => React.createElement('span', null, children),
    Avatar: nul,
    Row: box('row'),
    Col: box('col'),
    Highlight: ({ sourceString }) =>
      React.createElement('span', null, sourceString),
    Input: nul,
  };
});

import EditChannelModal from './EditChannelModal';

const renderModal = (over = {}) => {
  const props = {
    refresh: vi.fn(),
    visible: false,
    handleClose: vi.fn(),
    editingChannel: { id: undefined },
    ...over,
  };
  render(<EditChannelModal {...props} />);
  return props;
};

const fireVerificationSuccess = (result) =>
  act(() => {
    H.onSuccess.current(result);
  });

// Everything console.log was handed, flattened to text.
const loggedText = () =>
  logSpy.mock.calls
    .map((args) => args.map((a) => safeString(a)).join(' '))
    .join('\n');

const safeString = (v) => {
  if (typeof v === 'string') return v;
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
};

let logSpy;

beforeEach(() => {
  vi.clearAllMocks();
  H.onSuccess.current = null;
  H.apiGet.mockResolvedValue({ data: { success: true, data: [] } });
  H.apiPost.mockResolvedValue({ data: { success: true, data: [] } });
  logSpy = vi.spyOn(console, 'log').mockImplementation(() => {});
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('key reveal — the legitimate behaviour', () => {
  it('registers a verification success handler on mount', () => {
    renderModal();
    expect(typeof H.onSuccess.current).toBe('function');
  });

  it('shows the revealed key in the key display once verification succeeds', () => {
    renderModal();
    fireVerificationSuccess({ success: true, data: { key: SECRET } });

    expect(screen.getByTestId('key-display').dataset.key).toBe(SECRET);
    expect(H.showSuccess).toHaveBeenCalledWith('密钥获取成功');
  });

  it('accepts the unwrapped shape where the key sits at the top level', () => {
    renderModal();
    fireVerificationSuccess({ key: SECRET });
    expect(screen.getByTestId('key-display').dataset.key).toBe(SECRET);
  });

  it('shows no key modal at all when the result carries no key', () => {
    renderModal();
    fireVerificationSuccess({ success: true, data: {} });
    // Opening an empty "here is your key" modal would be worse than nothing.
    expect(screen.queryByTestId('key-modal')).toBeNull();
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('shows no key modal when verification reports failure', () => {
    renderModal();
    fireVerificationSuccess({ success: false, message: 'wrong code' });
    expect(screen.queryByTestId('key-modal')).toBeNull();
  });

  it('keeps the key out of the DOM until verification actually succeeds', () => {
    renderModal();
    expect(screen.queryByTestId('key-display')).toBeNull();
    expect(document.body.innerHTML).not.toContain(SECRET);
  });
});

// There are TWO reveal paths in this component, not one. The block above
// covers the step-up callback (`onSuccess`). This block covers the other:
// handleShow2FAModal, which awaits withVerification and — when the operator
// is ALREADY verified and the API answers inline — opens the key modal from
// its own separate guard. Deleting that guard passed the entire suite before
// these tests existed.
describe('key reveal — the already-verified direct path', () => {
  // loadChannel() string-splits models/group, so the fetch has to answer with a
  // channel-shaped record. A fresh copy per call: loadChannel mutates it.
  const channelRow = () => ({
    id: 5,
    name: 'openai-prod',
    type: 1,
    status: 1,
    key: '',
    base_url: '',
    models: '',
    group: '',
    model_mapping: '',
    setting: '',
    channel_info: {},
  });

  beforeEach(() => {
    H.apiGet.mockImplementation((url) =>
      String(url) === '/api/channel/5'
        ? Promise.resolve({ data: { success: true, data: channelRow() } })
        : Promise.resolve({ data: { success: true, data: [] } }),
    );
  });

  const openSheet = () =>
    renderModal({ visible: true, editingChannel: { id: 5 } });

  const clickReveal = async () => {
    const btn = (await screen.findAllByText('查看密钥'))[0];
    await act(async () => {
      fireEvent.click(btn);
    });
  };

  it('requests verification scoped to the channel being edited', async () => {
    H.withVerification.mockResolvedValue(undefined);
    openSheet();
    await clickReveal();
    // The channel id is what makes this reveal specific to one credential.
    expect(H.viewChannelKey).toHaveBeenCalledWith(5);
    expect(H.withVerification.mock.calls[0][0]).toEqual({
      kind: 'viewChannelKey',
      id: 5,
    });
  });

  it('shows the key when verification comes back already satisfied', async () => {
    H.withVerification.mockResolvedValue({
      success: true,
      data: { key: SECRET },
    });
    openSheet();
    await clickReveal();
    expect(screen.getByTestId('key-display').dataset.key).toBe(SECRET);
    expect(H.showSuccess).toHaveBeenCalledWith('密钥获取成功');
  });

  it('shows no key modal when the inline result carries no key', async () => {
    // Same contract as the onSuccess path: an empty "here is your key" modal
    // is worse than none. This guard lives separately here and had no test.
    H.withVerification.mockResolvedValue({ success: true, data: {} });
    openSheet();
    await clickReveal();
    expect(screen.queryByTestId('key-modal')).toBeNull();
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('shows no key modal when the inline result reports failure', async () => {
    H.withVerification.mockResolvedValue({
      success: false,
      data: { key: SECRET },
    });
    openSheet();
    await clickReveal();
    expect(screen.queryByTestId('key-modal')).toBeNull();
    expect(document.body.innerHTML).not.toContain(SECRET);
  });

  it('reports a failed reveal instead of silently doing nothing', async () => {
    H.withVerification.mockRejectedValue(new Error('verification cancelled'));
    openSheet();
    await clickReveal();
    expect(H.showError).toHaveBeenCalledWith('verification cancelled');
    expect(screen.queryByTestId('key-display')).toBeNull();
  });
});

describe('key reveal — credential handling', () => {
  // DEFECT (security): EditChannelModal.jsx:281, the first statement of the
  // verification success handler, is
  //     console.log('Verification success, result:', result);
  // and `result` is the object whose `.data.key` is the channel's live
  // upstream provider API key. So every successful step-up reveal writes the
  // plaintext credential into the browser console.
  //
  // Why it matters: the console survives the modal being closed, is captured
  // verbatim by console-forwarding / session-replay / error-reporting agents,
  // is visible in any screen share or support recording, and is dumped into
  // "save console output" bug reports. The whole point of putting this behind
  // a Passkey / 2FA step-up is that the key is only exposed to the operator
  // for as long as the modal is open; the log defeats that.
  //
  // Both reveal shapes go through the same log statement, so the lock covers
  // both — otherwise a redaction applied to only one of them would read as a
  // complete fix.
  //
  // Verified red 2026-08-20: un-skipped it fails, the logged text contains
  // the key. Verified green 2026-08-20 with line 281 deleted, so this is a
  // real oracle for the repair and not just a red mark.
  it('CONTRACT:never writes the revealed key to the console', () => {
    renderModal();
    fireVerificationSuccess({ success: true, data: { key: SECRET } });
    expect(loggedText()).not.toContain(SECRET);

    logSpy.mockClear();
    fireVerificationSuccess({ key: SECRET });
    expect(loggedText()).not.toContain(SECRET);
  });

  // There used to be two passing tests here asserting `loggedText()` DOES
  // contain the secret — one per shape. They added nothing the lock above
  // does not already state, and they turned red the moment the leak was
  // repaired (confirmed by deleting the console.log: both failed), so the
  // repair would have arrived looking like a regression. Deleted rather than
  // weakened; the defect is documented by the comment and the lock.

  it('does not additionally leak the key through a toast message', () => {
    renderModal();
    fireVerificationSuccess({ success: true, data: { key: SECRET } });
    const toasts = [
      ...H.showSuccess.mock.calls,
      ...H.showError.mock.calls,
      ...H.showInfo.mock.calls,
    ]
      .flat()
      .map(safeString)
      .join('\n');
    expect(toasts).not.toContain(SECRET);
  });
});
