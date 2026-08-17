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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

// utils.jsx evaluates window.matchMedia at module scope with no guard.
vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

// Semi UI's barrel needs a canvas (lottie-web). Only Toast and Pagination
// are used by utils.jsx; both are replaced with observable stand-ins so the
// assertions below are about WHICH sink got called with WHAT, which is the
// entire contract of the toast funnel.
vi.mock('@douyinfe/semi-ui', () => ({
  Toast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
  Pagination: (props) =>
    React.createElement('nav', {
      'data-testid': 'pagination',
      'data-current': String(props.currentPage),
      'data-page-size': String(props.pageSize),
      'data-total': String(props.total),
      'data-size': props.size,
      'data-quick-jumper': String(props.showQuickJumper),
      'data-size-changer': String(props.showSizeChanger),
    }),
}));

vi.mock('react-toastify', () => ({ toast: vi.fn() }));

vi.mock('../components/hifi/HfToast', () => ({
  hfToast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}));

import { Toast } from '@douyinfe/semi-ui';
import { toast } from 'react-toastify';
import { hfToast } from '../components/hifi/HfToast';
import { _resetDedup } from './errorMessages';
import HTMLToastContent, {
  showError,
  showWarning,
  showSuccess,
  showInfo,
  showNotice,
  openPage,
  copy,
  downloadTextAsFile,
  createCardProPagination,
  formatPriceInfo,
} from './utils';

const V1_PATH = '/console/tokens';
const V2_PATH = '/console/v2/billing';

const goTo = (path) => window.history.pushState({}, '', path);

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  _resetDedup();
  goTo(V1_PATH);
  // showError logs the raw error; keep the run output readable.
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('toast funnel routes by console generation', () => {
  it('legacy /console/* goes to the Semi Toast', () => {
    showSuccess('saved');
    showWarning('careful');
    showInfo('fyi');
    expect(Toast.success).toHaveBeenCalledWith('saved');
    expect(Toast.warning).toHaveBeenCalledWith('careful');
    expect(Toast.info).toHaveBeenCalledWith('fyi');
    expect(hfToast.success).not.toHaveBeenCalled();
  });

  it('/console/v2/* goes to the hi-fi toast instead', () => {
    goTo(V2_PATH);
    showSuccess('saved');
    showWarning('careful');
    showInfo('fyi');
    showError('boom');
    expect(hfToast.success).toHaveBeenCalledWith('saved');
    expect(hfToast.warning).toHaveBeenCalledWith('careful');
    expect(hfToast.info).toHaveBeenCalledWith('fyi');
    expect(hfToast.error).toHaveBeenCalledWith('boom');
    expect(Toast.success).not.toHaveBeenCalled();
    expect(Toast.error).not.toHaveBeenCalled();
  });

  it('the /console/v2 prefix match is a prefix, not an exact path', () => {
    goTo('/console/v2');
    showInfo('x');
    expect(hfToast.info).toHaveBeenCalledWith('x');
  });

  it('a lookalike path outside /console/v2 stays on the legacy toast', () => {
    goTo('/v2/console/billing');
    showInfo('x');
    expect(Toast.info).toHaveBeenCalledWith('x');
    expect(hfToast.info).not.toHaveBeenCalled();
  });
});

describe('showNotice', () => {
  it('plain text reuses the info toast', () => {
    showNotice('hello');
    expect(Toast.info).toHaveBeenCalledWith('hello');
    expect(toast).not.toHaveBeenCalled();
  });

  it('HTML notices go through react-toastify and never auto-close', () => {
    showNotice('<b>hi</b>', true);
    expect(toast).toHaveBeenCalledTimes(1);
    const [, options] = toast.mock.calls[0];
    expect(options.autoClose).toBe(false);
    expect(Toast.info).not.toHaveBeenCalled();
  });
});

describe('HTMLToastContent', () => {
  it('injects the raw markup it is given', () => {
    render(<HTMLToastContent htmlContent='<b>bold notice</b>' />);
    expect(screen.getByText('bold notice').tagName).toBe('B');
  });
});

describe('showError', () => {
  it('emits a plain string message once, then dedupes the repeat', () => {
    showError('copy failed');
    showError('copy failed');
    expect(Toast.error).toHaveBeenCalledTimes(1);
    expect(Toast.error).toHaveBeenCalledWith('copy failed');
  });

  it('emits a different message even inside the dedup window', () => {
    showError('first');
    showError('second');
    expect(Toast.error).toHaveBeenCalledTimes(2);
  });

  it('collapses repeated identical axios failures into a single toast', () => {
    const err = {
      isAxiosError: true,
      response: { status: 500, data: {} },
    };
    showError(err);
    showError({ ...err });
    showError({ ...err });
    expect(Toast.error).toHaveBeenCalledTimes(1);
  });

  it('prefers the backend human message for an unmapped status', () => {
    showError({
      isAxiosError: true,
      response: { status: 418, data: { message: 'pool exhausted' } },
    });
    expect(Toast.error).toHaveBeenCalledWith('pool exhausted');
  });

  it('stays silent (redirects instead) on a 401 with no local session', () => {
    localStorage.removeItem('user');
    showError({ isAxiosError: true, response: { status: 401, data: {} } });
    expect(Toast.error).not.toHaveBeenCalled();
    expect(hfToast.error).not.toHaveBeenCalled();
  });

  it('does toast a 401 when a local session shim still exists', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    showError({ isAxiosError: true, response: { status: 401, data: {} } });
    expect(Toast.error).toHaveBeenCalledTimes(1);
  });

  it('always logs the raw error for the console/debugger', () => {
    const err = new Error('detail for logs');
    showError(err);
    expect(console.error).toHaveBeenCalledWith(err);
  });
});

describe('openPage', () => {
  it('delegates to window.open', () => {
    const spy = vi.spyOn(window, 'open').mockImplementation(() => null);
    openPage('https://docs.example.test');
    expect(spy).toHaveBeenCalledWith('https://docs.example.test');
  });
});

describe('copy', () => {
  const withClipboard = (writeText) =>
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
      writable: true,
    });

  afterEach(() => {
    delete document.execCommand;
  });

  it('uses the async clipboard API when it works', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    withClipboard(writeText);
    document.execCommand = vi.fn();

    await expect(copy('sk-secret')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('sk-secret');
    expect(document.execCommand).not.toHaveBeenCalled();
  });

  it('falls back to a detached textarea when the clipboard API rejects', async () => {
    withClipboard(vi.fn().mockRejectedValue(new Error('denied')));
    let capturedValue = null;
    document.execCommand = vi.fn(() => {
      // The textarea must still be attached and selected at this point.
      const ta = document.querySelector('textarea');
      capturedValue = ta ? ta.value : null;
      return true;
    });

    await expect(copy('line1\nline2')).resolves.toBe(true);
    expect(document.execCommand).toHaveBeenCalledWith('copy');
    // Multi-line text must survive the fallback verbatim.
    expect(capturedValue).toBe('line1\nline2');
    // …and the scratch node must not be left behind in the DOM.
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('reports failure when both the API and the fallback fail', async () => {
    withClipboard(vi.fn().mockRejectedValue(new Error('denied')));
    document.execCommand = vi.fn(() => {
      throw new Error('unsupported');
    });

    await expect(copy('x')).resolves.toBe(false);
    expect(console.error).toHaveBeenCalled();
  });
});

describe('downloadTextAsFile', () => {
  it('wires a blob url onto a one-shot anchor with the requested filename', () => {
    const created = [];
    const realCreate = document.createElement.bind(document);
    vi.spyOn(document, 'createElement').mockImplementation((tag) => {
      const el = realCreate(tag);
      if (tag === 'a') {
        el.click = vi.fn();
        created.push(el);
      }
      return el;
    });
    const createObjectURL = vi.fn(() => 'blob:fake-url');
    Object.defineProperty(URL, 'createObjectURL', {
      value: createObjectURL,
      configurable: true,
      writable: true,
    });

    downloadTextAsFile('id,name\n1,a', 'export.csv');

    expect(created).toHaveLength(1);
    expect(created[0].download).toBe('export.csv');
    expect(created[0].getAttribute('href')).toBe('blob:fake-url');
    expect(created[0].click).toHaveBeenCalledTimes(1);
    // The blob must carry the text and a utf-8 charset, or Excel mangles CJK.
    const blob = createObjectURL.mock.calls[0][0];
    expect(blob.type).toBe('text/plain;charset=utf-8');
  });
});

describe('createCardProPagination', () => {
  const base = {
    currentPage: 2,
    pageSize: 10,
    onPageChange: vi.fn(),
    onPageSizeChange: vi.fn(),
  };

  it('renders nothing at all when there is no data', () => {
    expect(createCardProPagination({ ...base, total: 0 })).toBeNull();
    expect(createCardProPagination({ ...base, total: undefined })).toBeNull();
    expect(createCardProPagination({ ...base, total: -1 })).toBeNull();
  });

  it('computes a 1-based inclusive range for a middle page', () => {
    render(<div>{createCardProPagination({ ...base, total: 25 })}</div>);
    // page 2 of size 10 -> items 11..20
    expect(screen.getByText('显示第 11 条 - 第 20 条，共 25 条')).toBeTruthy();
  });

  it('clamps the range end to the total on the last page', () => {
    render(
      <div>
        {createCardProPagination({ ...base, currentPage: 3, total: 25 })}
      </div>,
    );
    expect(screen.getByText('显示第 21 条 - 第 25 条，共 25 条')).toBeTruthy();
  });

  it('hides the count line and shrinks the control on mobile', () => {
    render(
      <div>
        {createCardProPagination({ ...base, total: 25, isMobile: true })}
      </div>,
    );
    expect(screen.queryByText(/显示第/)).toBeNull();
    const nav = screen.getByTestId('pagination');
    expect(nav.getAttribute('data-size')).toBe('small');
    expect(nav.getAttribute('data-quick-jumper')).toBe('true');
  });

  it('passes page geometry straight through to the control', () => {
    render(<div>{createCardProPagination({ ...base, total: 25 })}</div>);
    const nav = screen.getByTestId('pagination');
    expect(nav.getAttribute('data-current')).toBe('2');
    expect(nav.getAttribute('data-page-size')).toBe('10');
    expect(nav.getAttribute('data-total')).toBe('25');
    expect(nav.getAttribute('data-size')).toBe('default');
    expect(nav.getAttribute('data-size-changer')).toBe('true');
  });

  it('runs the caller-supplied translator over every label fragment', () => {
    render(
      <div>
        {createCardProPagination({
          ...base,
          total: 25,
          t: (k) => `[${k}]`,
        })}
      </div>,
    );
    expect(
      screen.getByText('[显示第] 11 [条 - 第] 20 [条，共] 25 [条]'),
    ).toBeTruthy();
  });
});

describe('formatPriceInfo', () => {
  const t = (k) => k;

  it('shows both input and output legs for per-token pricing', () => {
    render(
      <div>
        {formatPriceInfo(
          {
            isPerToken: true,
            inputPrice: '$1.0000',
            completionPrice: '$3.0000',
            unitLabel: 'M',
          },
          t,
        )}
      </div>,
    );
    expect(screen.getByText('输入 $1.0000/M')).toBeTruthy();
    expect(screen.getByText('输出 $3.0000/M')).toBeTruthy();
  });

  it('shows a single flat price for per-call pricing', () => {
    render(
      <div>{formatPriceInfo({ isPerToken: false, price: '$0.040' }, t)}</div>,
    );
    expect(screen.getByText('模型价格 $0.040')).toBeTruthy();
    expect(screen.queryByText(/输入/)).toBeNull();
  });
});
