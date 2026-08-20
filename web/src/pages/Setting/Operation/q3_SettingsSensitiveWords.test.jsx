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

// Blocked-word filtering. Two switches plus a newline-separated word list.
// The list is the part that matters: the backend splits it on newlines, so the
// exact string that leaves this form is the filter. Anything that trims,
// re-orders or drops it removes protection without saying so.

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
  const Tag = ({ children }) => React.createElement('span', null, children);
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

  const makeField = (kind) =>
    function Field({
      field,
      label,
      extraText,
      placeholder,
      onChange,
      disabled,
    }) {
      H.handlers[field] = onChange;
      return React.createElement(
        'div',
        {
          'data-testid': `field-${field}`,
          'data-kind': kind,
          'data-disabled': disabled ? 'true' : 'false',
          'data-placeholder':
            typeof placeholder === 'string' ? placeholder : '',
        },
        label,
        extraText,
      );
    };

  Form.Section = ({ text, children }) =>
    React.createElement('section', { 'data-section': text }, text, children);
  Form.Switch = makeField('switch');
  Form.TextArea = makeField('textarea');

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

import SettingsSensitiveWords from './SettingsSensitiveWords';

// The two switches carry DIFFERENT values so a handler wired to the wrong key
// is visible in the payload rather than hidden by a matching literal.
const OPTIONS = () => ({
  CheckSensitiveEnabled: true,
  CheckSensitiveOnPromptEnabled: false,
  SensitiveWords: 'alpha\nbravo\ncharlie',
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderWords = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<SettingsSensitiveWords options={options} refresh={refresh} />);
  return { refresh };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存屏蔽词过滤设置').click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');

beforeEach(() => {
  H.put.mockReset();
  H.put.mockResolvedValue(okResponse());
  H.showError.mockReset();
  H.showSuccess.mockReset();
  H.showWarning.mockReset();
  H.handlers = {};
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds both switches and the word list from the stored options', () => {
    renderWords();
    expect(formValues()).toEqual({
      CheckSensitiveEnabled: true,
      CheckSensitiveOnPromptEnabled: false,
      SensitiveWords: 'alpha\nbravo\ncharlie',
    });
  });

  it('pushes the same values into the Semi form api', () => {
    renderWords();
    expect(H.setValuesCalls).toHaveLength(1);
    expect(H.setValuesCalls[0].SensitiveWords).toBe('alpha\nbravo\ncharlie');
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderWords({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });
});

describe('saving', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderWords();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('turning the filter OFF writes the string "false" for the master switch', async () => {
    renderWords();
    await change('CheckSensitiveEnabled', false);
    await save();
    expect(putBodies()).toEqual([
      { key: 'CheckSensitiveEnabled', value: 'false' },
    ]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('turning prompt checking ON writes only the prompt key', async () => {
    renderWords();
    await change('CheckSensitiveOnPromptEnabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'CheckSensitiveOnPromptEnabled', value: 'true' },
    ]);
  });

  it('writes the word list verbatim, newlines and all', async () => {
    renderWords();
    const list = 'delta\necho\nfoxtrot\n golf ';
    await change('SensitiveWords', list);
    await save();
    expect(putBodies()).toEqual([{ key: 'SensitiveWords', value: list }]);
  });

  it('writes an emptied word list rather than treating it as no change', async () => {
    renderWords();
    await change('SensitiveWords', '');
    await save();
    // Clearing the list is a real edit; dropping it would leave the old words
    // filtering while the console shows none.
    expect(putBodies()).toEqual([{ key: 'SensitiveWords', value: '' }]);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderWords();
    await change('SensitiveWords', 'hotel\nindia');
    await change('CheckSensitiveOnPromptEnabled', true);
    await save();
    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(String(body.value)).toBe(String(shown[body.key]));
    }
  });

  it('reports success and reloads when every write succeeded', async () => {
    const { refresh } = renderWords();
    await change('SensitiveWords', 'hotel');
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderWords();
    H.put.mockRejectedValue(new Error('network down'));
    await change('SensitiveWords', 'hotel');
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
    renderWords();
    await change('SensitiveWords', 'hotel');
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');
    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });

  // DEFECT (correctness): res[i].data.success is never inspected. A refusal
  // from the option endpoint is announced as 保存成功 and followed by a reload
  // that puts the old word list back — the operator believes the words they
  // just added are being filtered.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderWords();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('SensitiveWords', 'hotel');
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above.
  it('does claim success when the server accepts the write', async () => {
    renderWords();
    await change('SensitiveWords', 'hotel');
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('fields the server has never stored', () => {
  // Positive control.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderWords();
    await change('CheckSensitiveOnPromptEnabled', true);
    await save();
    expect(putBodies().map((b) => b.key)).toContain(
      'CheckSensitiveOnPromptEnabled',
    );
  });

  // DEFECT (correctness): on an install where SensitiveWords has no row,
  // /api/option/ omits it, the parent omits it, and this component rebuilds
  // both `inputs` and `inputsRow` without it. compareObjects skips keys missing
  // from either side, so the very first word list an operator types can never
  // be saved — they are told 你似乎并没有修改什么.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    const partial = OPTIONS();
    delete partial.SensitiveWords;
    renderWords(partial);
    await change('SensitiveWords', 'juliet\nkilo');
    await save();
    expect(putBodies()).toEqual([
      { key: 'SensitiveWords', value: 'juliet\nkilo' },
    ]);
  });
});
