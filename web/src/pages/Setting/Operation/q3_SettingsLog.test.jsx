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

// Log settings holds one switch and one irreversible action: DELETE /api/log/
// with a cut-off timestamp, which destroys every consumption record before it.
// Two things are asserted hard here — that the destructive call cannot happen
// on a click alone (it must go through the confirm), and that the timestamp on
// the wire is the one the operator picked, in whole seconds.
//
// The date picker's value must also never be written to /api/option/: it is a
// local UI value, not a stored setting.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => {
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
    del: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    confirm: vi.fn(),
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
    API: { put: (...a) => H.put(...a), delete: (...a) => H.del(...a) },
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
      {
        'data-testid': 'form',
        'data-values': JSON.stringify(values ?? null),
      },
      children,
    );
  };

  const makeField = (kind) =>
    function Field({ field, label, onChange, disabled }) {
      H.handlers[field] = onChange;
      return React.createElement(
        'div',
        {
          'data-testid': `field-${field}`,
          'data-kind': kind,
          'data-disabled': disabled ? 'true' : 'false',
        },
        label,
      );
    };

  Form.Section = ({ text, children }) =>
    React.createElement('section', { 'data-section': text }, text, children);
  Form.Switch = makeField('switch');
  Form.DatePicker = makeField('date');

  return {
    Button,
    Col,
    Form,
    Row,
    Spin,
    DatePicker: () => null,
    Modal: { confirm: (...a) => H.confirm(...a) },
    Typography: {
      Text: ({ children, ...rest }) =>
        React.createElement('span', rest, children),
    },
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

import SettingsLog from './SettingsLog';

const OPTIONS = () => ({ LogConsumeEnabled: true });

const okResponse = () => ({ data: { success: true, message: '' } });

const renderLog = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<SettingsLog options={options} refresh={refresh} />);
  return { refresh };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存日志设置').click();
  });
};

// The date picker's label is the same string as the button, so this has to go
// by role rather than by text.
const clickClean = async () => {
  await act(async () => {
    screen.getByRole('button', { name: '清除历史日志' }).click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');
const lastConfirm = () => H.confirm.mock.calls.at(-1)[0];

// A fixed, unambiguous cut-off: 2026-03-04T05:06:07.891Z. The milliseconds are
// deliberately non-zero so a timestamp that is not reduced to whole seconds is
// visible in the URL.
const CUTOFF = new Date(Date.UTC(2026, 2, 4, 5, 6, 7, 891));
const CUTOFF_SECONDS = Math.floor(CUTOFF.getTime() / 1000);

beforeEach(() => {
  H.put.mockReset();
  H.put.mockResolvedValue(okResponse());
  H.del.mockReset();
  H.del.mockResolvedValue({ data: { success: true, message: '', data: 4321 } });
  H.showError.mockReset();
  H.showSuccess.mockReset();
  H.showWarning.mockReset();
  H.confirm.mockReset();
  H.handlers = {};
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds the consumption-log switch from the stored option', () => {
    renderLog();
    expect(formValues().LogConsumeEnabled).toBe(true);
  });

  it('keeps a date in the picker without needing one from the server', () => {
    renderLog();
    // historyTimestamp is a local UI value; the server never supplies it and
    // an empty picker would make the clean button unusable.
    expect(formValues().historyTimestamp).toBeTruthy();
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderLog({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });
});

describe('saving the log setting', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderLog();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
  });

  it('turning consumption logging OFF writes the string "false"', async () => {
    renderLog();
    await change('LogConsumeEnabled', false);
    await save();
    expect(putBodies()).toEqual([{ key: 'LogConsumeEnabled', value: 'false' }]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('never writes the date picker to the option store', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await save();
    // historyTimestamp is not an option; writing it would create a junk row
    // and, on the next load, a permanent phantom "unsaved change".
    expect(putBodies().map((b) => b.key)).not.toContain('historyTimestamp');
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
  });

  it('still saves the switch when the date was also touched', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await change('LogConsumeEnabled', false);
    await save();
    expect(putBodies()).toEqual([{ key: 'LogConsumeEnabled', value: 'false' }]);
  });

  it('reports success and reloads when the write succeeded', async () => {
    const { refresh } = renderLog();
    await change('LogConsumeEnabled', false);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderLog();
    H.put.mockRejectedValue(new Error('network down'));
    await change('LogConsumeEnabled', false);
    await save();
    await waitFor(() => expect(H.showError).toHaveBeenCalled());
    expect(H.showError).toHaveBeenCalledWith('保存失败，请重试');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // DEFECT (correctness): res[i].data.success is never inspected, so a refused
  // write is announced as 保存成功. An operator who has just switched
  // consumption logging off — the switch that decides whether usage is billed
  // and recorded at all — is told it worked when it did not.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderLog();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('LogConsumeEnabled', false);
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above.
  it('does claim success when the server accepts the write', async () => {
    renderLog();
    await change('LogConsumeEnabled', false);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('clearing history — the irreversible path', () => {
  it('refuses to go anywhere near the API with no date chosen', async () => {
    renderLog();
    await change('historyTimestamp', null);
    await clickClean();
    expect(H.showError).toHaveBeenCalledWith('请选择日志记录时间');
    expect(H.confirm).not.toHaveBeenCalled();
    expect(H.del).not.toHaveBeenCalled();
  });

  it('does not delete anything on the button click alone', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    expect(H.confirm).toHaveBeenCalledTimes(1);
    expect(H.del).not.toHaveBeenCalled();
  });

  it('offers a danger-styled confirm naming the cut-off time', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    const cfg = lastConfirm();
    expect(cfg.okType).toBe('danger');
    expect(cfg.okText).toBe('确认删除');
    expect(cfg.cancelText).toBe('取消');

    const { container } = render(<div>{cfg.content}</div>);
    // The operator must see the exact moment they are cutting at, plus the
    // "cannot be undone" line, before the destructive call is possible.
    expect(container.textContent).toContain('2026-03-04');
    expect(container.textContent).toContain(
      '此操作不可恢复，请仔细确认时间后再操作！',
    );
  });

  it('deletes with the chosen cut-off in whole unix seconds once confirmed', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    await act(async () => {
      await lastConfirm().onOk();
    });
    expect(H.del).toHaveBeenCalledTimes(1);
    expect(H.del.mock.calls[0][0]).toBe(
      `/api/log/?target_timestamp=${CUTOFF_SECONDS}`,
    );
    // A fractional or millisecond timestamp would be a different cut-off than
    // the one the confirm dialog showed.
    expect(H.del.mock.calls[0][0]).not.toMatch(/\./);
  });

  it('does not delete when the operator cancels the confirm', async () => {
    renderLog();
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    // Cancelling simply never invokes onOk.
    expect(H.del).not.toHaveBeenCalled();
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('reports how many rows the server actually removed', async () => {
    renderLog();
    H.del.mockResolvedValue({
      data: { success: true, message: '', data: 4321 },
    });
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    await act(async () => {
      await lastConfirm().onOk();
    });
    expect(H.showSuccess).toHaveBeenCalledWith('4321 条日志已清理！');
  });

  it('surfaces the server refusal instead of claiming a clean-up', async () => {
    renderLog();
    H.del.mockResolvedValue({
      data: { success: false, message: 'log table is locked', data: 0 },
    });
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    await act(async () => {
      await lastConfirm().onOk();
    });
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalledWith(
      '日志清理失败：log table is locked',
    );
  });

  it('surfaces a transport failure instead of claiming a clean-up', async () => {
    renderLog();
    H.del.mockRejectedValue(new Error('network down'));
    await change('historyTimestamp', CUTOFF);
    await clickClean();
    await act(async () => {
      await lastConfirm().onOk();
    });
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalledWith('network down');
  });

  // Positive control for the lock below: a cut-off in the past deletes, and
  // the confirm tells the operator how far back it reaches.
  it('annotates how old the cut-off is when it is in the past', async () => {
    renderLog();
    const tenDaysAgo = new Date(Date.now() - 10 * 24 * 3600 * 1000);
    await change('historyTimestamp', tenDaysAgo);
    await clickClean();
    const { container } = render(<div>{lastConfirm().content}</div>);
    expect(container.textContent).toContain('10');
    expect(container.textContent).toContain('天前');
  });

  // DEFECT (data-loss): nothing rejects a cut-off in the FUTURE. "Delete every
  // log before <a moment that has not happened>" is the whole table, including
  // the records written a second ago. It is also the one case where the confirm
  // dialog goes quiet: the "约 N 天前" annotation is behind `daysDiff > 0`, so
  // for a future date the operator sees only a timestamp with no indication of
  // how much is about to go. The call is irreversible and there is no backup
  // path in the console.
  it.skip('refuses a cut-off in the future', async () => {
    renderLog();
    const tomorrow = new Date(Date.now() + 24 * 3600 * 1000);
    await change('historyTimestamp', tomorrow);
    await clickClean();
    expect(H.confirm).not.toHaveBeenCalled();
    expect(H.del).not.toHaveBeenCalled();
  });
});

describe('fields the server has never stored', () => {
  // Positive control.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderLog();
    await change('LogConsumeEnabled', false);
    await save();
    expect(putBodies().map((b) => b.key)).toContain('LogConsumeEnabled');
  });

  // DEFECT (correctness): compareObjects skips any key missing from either
  // side, and `inputsRow` is built from only the keys /api/option/ returned.
  // On an install that has never stored LogConsumeEnabled, the switch can be
  // flipped in the UI but never written — the operator is told
  // 你似乎并没有修改什么 and consumption logging stays at the backend default.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    renderLog({});
    await change('LogConsumeEnabled', true);
    await save();
    expect(putBodies()).toEqual([{ key: 'LogConsumeEnabled', value: 'true' }]);
  });
});
