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

// Six drawing/Midjourney switches. Two of them are not cosmetic: MjNotifyEnabled
// leaks the server IP when on, and it is ALSO mirrored into localStorage under
// `mj_notify_enabled`, which src/hooks/mj-logs/useMjLogsData.js reads to decide
// what it shows. So this page is a writer of shared client state, not only of
// server options — the mirror is tested here as part of the round trip.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => {
  // helpers/utils.jsx reads matchMedia at module scope; jsdom has no
  // implementation. Installed here so the REAL compareObjects can be imported
  // below instead of being re-typed into the test.
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = () => ({
      matches: false,
      media: '',
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    });
  }
  return {
    put: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    handlers: {},
    setValuesCalls: [],
  };
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers/utils');
  return {
    API: { put: (...a) => H.put(...a) },
    showError: (...a) => H.showError(...a),
    showSuccess: (...a) => H.showSuccess(...a),
    showWarning: (...a) => H.showWarning(...a),
    compareObjects: actual.compareObjects,
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ children, onClick, loading, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        'data-loading': loading ? 'true' : 'false',
        ...rest,
      },
      children,
    );

  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);
  const Tag = ({ children }) =>
    React.createElement('span', { 'data-testid': 'tag' }, children);
  const Spin = ({ children, spinning }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': spinning ? 'true' : 'false' },
      children,
    );

  const Form = ({ children, values, getFormApi }) => {
    const apiRef = React.useRef(null);
    if (!apiRef.current) {
      apiRef.current = { setValues: (v) => H.setValuesCalls.push(v) };
    }
    if (getFormApi) getFormApi(apiRef.current);
    return React.createElement(
      'form',
      { 'data-testid': 'form', 'data-values': JSON.stringify(values ?? null) },
      children,
    );
  };

  Form.Section = ({ text, children }) =>
    React.createElement('section', { 'data-section': text }, text, children);
  Form.Switch = function Switch({ field, label, onChange, disabled }) {
    H.handlers[field] = onChange;
    return React.createElement(
      'div',
      {
        'data-testid': `field-${field}`,
        'data-kind': 'switch',
        'data-disabled': disabled ? 'true' : 'false',
      },
      label,
    );
  };

  return {
    Button,
    Col,
    Form,
    Row,
    Spin,
    Tag,
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

import SettingsDrawing from './SettingsDrawing';

// Not all-true and not all-false: a handler wired to the wrong switch, or a
// payload built from the component's defaults rather than the loaded options,
// produces a visibly different result.
const OPTIONS = () => ({
  DrawingEnabled: true,
  MjNotifyEnabled: false,
  MjAccountFilterEnabled: true,
  MjForwardUrlEnabled: false,
  MjModeClearEnabled: true,
  MjActionCheckSuccessEnabled: false,
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderDrawing = (options = OPTIONS()) => {
  const refresh = vi.fn();
  const view = render(<SettingsDrawing options={options} refresh={refresh} />);
  return { refresh, view };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存绘图设置').click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');

beforeEach(() => {
  localStorage.clear();
  H.put.mockReset();
  H.put.mockResolvedValue(okResponse());
  H.showError.mockReset();
  H.showSuccess.mockReset();
  H.showWarning.mockReset();
  H.handlers = {};
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds every switch from the stored option, not from the defaults', () => {
    renderDrawing();
    expect(formValues()).toEqual(OPTIONS());
  });

  it('renders all six switches', () => {
    renderDrawing();
    for (const field of Object.keys(OPTIONS())) {
      expect(screen.getByTestId(`field-${field}`)).toBeInTheDocument();
    }
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderDrawing({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });

  it('warns in the label that the callback switch leaks the server IP', () => {
    renderDrawing();
    expect(screen.getByTestId('field-MjNotifyEnabled')).toHaveTextContent(
      '泄露服务器 IP 地址',
    );
  });
});

describe('saving', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderDrawing();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('turning a switch ON writes the string "true" for that key only', async () => {
    renderDrawing();
    await change('MjForwardUrlEnabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'MjForwardUrlEnabled', value: 'true' },
    ]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('turning a switch OFF writes the string "false", not a dropped key', async () => {
    renderDrawing();
    await change('MjAccountFilterEnabled', false);
    await save();
    expect(putBodies()).toEqual([
      { key: 'MjAccountFilterEnabled', value: 'false' },
    ]);
  });

  it('writes one request per changed switch and leaves the rest alone', async () => {
    renderDrawing();
    await change('DrawingEnabled', false);
    await change('MjNotifyEnabled', true);
    await save();

    const bodies = putBodies().sort((a, b) => a.key.localeCompare(b.key));
    expect(bodies).toEqual([
      { key: 'DrawingEnabled', value: 'false' },
      { key: 'MjNotifyEnabled', value: 'true' },
    ]);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderDrawing();
    await change('DrawingEnabled', false);
    await change('MjModeClearEnabled', false);
    await save();

    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(body.value).toBe(String(shown[body.key]));
    }
  });

  it('reports success and reloads when every write succeeded', async () => {
    const { refresh } = renderDrawing();
    await change('DrawingEnabled', false);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderDrawing();
    H.put.mockRejectedValue(new Error('network down'));
    await change('DrawingEnabled', false);
    await save();
    await waitFor(() => expect(H.showError).toHaveBeenCalled());
    expect(H.showError).toHaveBeenCalledWith('保存失败，请重试');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('shows the page busy while the writes are in flight and idle after', async () => {
    let release;
    H.put.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve(okResponse());
        }),
    );
    renderDrawing();
    await change('DrawingEnabled', false);
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');
    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });

  // DEFECT (correctness): unlike SettingsRequestRateLimit, this component never
  // inspects res[i].data.success. A 200 response carrying {success:false} — the
  // shape the option endpoint uses to refuse a write — is reported to the
  // operator as 保存成功 and triggers a reload that quietly restores the old
  // value. The switch the operator believes they just turned off is still on.
  // The assertion below is the invariant that holds under any fix: a refused
  // write must not be announced as a successful one.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderDrawing();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('DrawingEnabled', false);
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above: a genuinely accepted write must still
  // be announced, so the lock cannot be satisfied by never reporting success.
  it('does claim success when the server accepts the write', async () => {
    renderDrawing();
    await change('DrawingEnabled', false);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('the mj_notify_enabled mirror in localStorage', () => {
  it('writes the flag as the string form of a boolean', () => {
    renderDrawing();
    // useMjLogsData compares this against the literal 'true', so anything
    // other than 'true'/'false' silently reads as off.
    expect(['true', 'false']).toContain(
      localStorage.getItem('mj_notify_enabled'),
    );
  });

  // DEFECT (correctness): the effect writes String(inputs.MjNotifyEnabled) —
  // `inputs` is the state from the render that is being replaced, not the
  // `currentInputs` it just computed from props.options. The mirror is
  // therefore always one load behind. src/hooks/mj-logs/useMjLogsData.js reads
  // it, so an admin who opens the drawing settings page poisons their own MJ
  // logs view with the previous value of the flag.
  it.skip('mirrors the option value that was just loaded', () => {
    renderDrawing({ ...OPTIONS(), MjNotifyEnabled: true });
    expect(localStorage.getItem('mj_notify_enabled')).toBe('true');
  });

  // Same defect seen through a second load: a NEW options object arrives with
  // the flag flipped, and the mirror still shows the value from the load
  // before it.
  it.skip('updates the mirror when a later load changes the flag', () => {
    const { view } = renderDrawing({ ...OPTIONS(), MjNotifyEnabled: false });
    view.rerender(
      <SettingsDrawing
        options={{ ...OPTIONS(), MjNotifyEnabled: true }}
        refresh={vi.fn()}
      />,
    );
    expect(localStorage.getItem('mj_notify_enabled')).toBe('true');
  });
});

describe('fields the server has never stored', () => {
  // Positive control: a key that came back from /api/option/ saves normally.
  it('saves an edit to a switch that was present in the loaded options', async () => {
    renderDrawing();
    await change('MjActionCheckSuccessEnabled', true);
    await save();
    expect(putBodies().map((b) => b.key)).toContain(
      'MjActionCheckSuccessEnabled',
    );
  });

  // DEFECT (correctness): the load effect rebuilds `inputs` from only the keys
  // present in props.options and snapshots the same reduced object into
  // `inputsRow`; compareObjects skips any key missing from either side. A
  // switch the server has never stored can be toggled in the UI but can never
  // be written — the operator is told 你似乎并没有修改什么 and the feature stays
  // in whatever state the backend default happens to be.
  it.skip('saves an edit to a switch that was absent from the loaded options', async () => {
    const partial = OPTIONS();
    delete partial.MjActionCheckSuccessEnabled;
    renderDrawing(partial);
    await change('MjActionCheckSuccessEnabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'MjActionCheckSuccessEnabled', value: 'true' },
    ]);
  });
});
