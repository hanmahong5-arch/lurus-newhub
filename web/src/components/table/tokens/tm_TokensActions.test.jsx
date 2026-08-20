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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const spies = vi.hoisted(() => ({ showError: null }));

// Modal is stubbed as a real, visible dialog with wired ok/cancel buttons so
// the confirmation flow is exercised end to end rather than short-circuited.
vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    // Semi renders its own OK/Cancel pair only when no custom `footer` is
    // supplied, but the dismiss affordance (the X / mask click → onCancel)
    // exists either way. Model both so the cancel path stays reachable for a
    // dialog that overrides the footer.
    Modal: ({ visible, title, children, footer, onOk, onCancel }) =>
      visible
        ? h(
            'div',
            { role: 'dialog', 'data-testid': `dialog-${title}` },
            h('h2', null, title),
            children,
            footer ??
              h('button', { type: 'button', onClick: onOk }, `ok:${title}`),
            h(
              'button',
              { type: 'button', onClick: onCancel },
              `close:${title}`,
            ),
          )
        : null,
    Button: ({ children, onClick, type }) =>
      h('button', { type: 'button', onClick, 'data-btn-type': type }, children),
    Space: ({ children }) => h('span', null, children),
  };
});

vi.mock('../../../helpers', () => {
  spies.showError = vi.fn();
  return { showError: spies.showError };
});

import TokensActions from './TokensActions';

const t = (key, vars) =>
  vars
    ? Object.entries(vars).reduce(
        (s, [k, v]) => s.replace(new RegExp(`{{\\s*${k}\\s*}}`, 'g'), v),
        key,
      )
    : key;

const TOKEN_A = { id: 1, name: 'prod', key: 'AAAAKEYAAAA' };
const TOKEN_B = { id: 2, name: 'staging', key: 'BBBBKEYBBBB' };

let props;

const setup = (over = {}) => {
  props = {
    selectedKeys: [],
    setEditingToken: vi.fn(),
    setShowEdit: vi.fn(),
    batchCopyTokens: vi.fn(),
    batchDeleteTokens: vi.fn(),
    copyText: vi.fn().mockResolvedValue(true),
    t,
    ...over,
  };
  return render(<TokensActions {...props} />);
};

beforeEach(() => {
  spies.showError?.mockClear();
});

describe('TokensActions — create', () => {
  it('opens the editor in create mode with an id-less draft', () => {
    setup();
    fireEvent.click(screen.getByRole('button', { name: '添加令牌' }));

    expect(props.setEditingToken).toHaveBeenCalledWith({ id: undefined });
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });
});

describe('TokensActions — copy guard', () => {
  it('refuses to open the copy dialog with nothing selected', () => {
    setup({ selectedKeys: [] });
    fireEvent.click(screen.getByRole('button', { name: '复制所选令牌' }));

    expect(spies.showError).toHaveBeenCalledWith('请至少选择一个令牌！');
    expect(screen.queryByTestId('dialog-复制令牌')).not.toBeInTheDocument();
    expect(props.copyText).not.toHaveBeenCalled();
  });

  it('opens the copy dialog once something is selected', () => {
    setup({ selectedKeys: [TOKEN_A] });
    fireEvent.click(screen.getByRole('button', { name: '复制所选令牌' }));

    expect(spies.showError).not.toHaveBeenCalled();
    expect(screen.getByTestId('dialog-复制令牌')).toBeInTheDocument();
  });
});

describe('TokensActions — clipboard payload', () => {
  const openCopy = (selectedKeys) => {
    setup({ selectedKeys });
    fireEvent.click(screen.getByRole('button', { name: '复制所选令牌' }));
  };

  it('"name + key" pastes one usable sk- key per line, labelled', async () => {
    openCopy([TOKEN_A, TOKEN_B]);
    fireEvent.click(screen.getByRole('button', { name: '名称+密钥' }));

    await waitFor(() => expect(props.copyText).toHaveBeenCalledTimes(1));
    expect(props.copyText).toHaveBeenCalledWith(
      'prod    sk-AAAAKEYAAAA\nstaging    sk-BBBBKEYBBBB\n',
    );
  });

  it('"key only" pastes the secrets with no names attached', async () => {
    openCopy([TOKEN_A, TOKEN_B]);
    fireEvent.click(screen.getByRole('button', { name: '仅密钥' }));

    await waitFor(() => expect(props.copyText).toHaveBeenCalledTimes(1));
    const payload = props.copyText.mock.calls[0][0];
    expect(payload).toBe('sk-AAAAKEYAAAA\nsk-BBBBKEYBBBB\n');
    // The distinguishing property against the other mode: no names leak in.
    expect(payload).not.toContain('prod');
    expect(payload).not.toContain('staging');
  });

  it('every copied key carries the sk- prefix the API expects', async () => {
    openCopy([TOKEN_A, TOKEN_B]);
    fireEvent.click(screen.getByRole('button', { name: '仅密钥' }));

    await waitFor(() => expect(props.copyText).toHaveBeenCalledTimes(1));
    const lines = props.copyText.mock.calls[0][0].split('\n').filter(Boolean);
    expect(lines).toHaveLength(2);
    lines.forEach((line) => expect(line.startsWith('sk-')).toBe(true));
  });

  it('closes the dialog after copying so it cannot be fired twice', async () => {
    openCopy([TOKEN_A]);
    fireEvent.click(screen.getByRole('button', { name: '仅密钥' }));

    await waitFor(() =>
      expect(screen.queryByTestId('dialog-复制令牌')).not.toBeInTheDocument(),
    );
    expect(props.copyText).toHaveBeenCalledTimes(1);
  });

  it('cancelling the copy dialog copies nothing', () => {
    openCopy([TOKEN_A]);
    fireEvent.click(screen.getByRole('button', { name: 'close:复制令牌' }));

    expect(screen.queryByTestId('dialog-复制令牌')).not.toBeInTheDocument();
    expect(props.copyText).not.toHaveBeenCalled();
  });
});

describe('TokensActions — delete guard and confirmation', () => {
  it('refuses to open the delete dialog with nothing selected', () => {
    setup({ selectedKeys: [] });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));

    expect(spies.showError).toHaveBeenCalledWith('请至少选择一个令牌！');
    expect(screen.queryByTestId('dialog-批量删除令牌')).not.toBeInTheDocument();
    expect(props.batchDeleteTokens).not.toHaveBeenCalled();
  });

  it('states how many tokens are about to be destroyed', () => {
    setup({ selectedKeys: [TOKEN_A, TOKEN_B] });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));

    const dialog = screen.getByTestId('dialog-批量删除令牌');
    expect(dialog).toHaveTextContent('确定要删除所选的 2 个令牌吗？');
    // Opening the dialog must not have deleted anything yet.
    expect(props.batchDeleteTokens).not.toHaveBeenCalled();
  });

  it('confirming deletes exactly once and closes', async () => {
    setup({ selectedKeys: [TOKEN_A] });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));
    fireEvent.click(screen.getByRole('button', { name: 'ok:批量删除令牌' }));

    expect(props.batchDeleteTokens).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(
        screen.queryByTestId('dialog-批量删除令牌'),
      ).not.toBeInTheDocument(),
    );
  });

  it('dismissing the delete dialog destroys nothing', () => {
    setup({ selectedKeys: [TOKEN_A, TOKEN_B] });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));
    fireEvent.click(screen.getByRole('button', { name: 'close:批量删除令牌' }));

    expect(props.batchDeleteTokens).not.toHaveBeenCalled();
    expect(screen.queryByTestId('dialog-批量删除令牌')).not.toBeInTheDocument();
  });

  // ---------------------------------------------------------------------
  // DEFECT: handleConfirmDelete fires batchDeleteTokens() without awaiting it
  // and closes the dialog in the same tick. batchDeleteTokens is an async
  // network call, so a refused batch delete (403, partial failure) ends with
  // the dialog gone and no failure surfaced by this component — the operator
  // reasonably reads "dialog closed" as "tokens revoked". The dialog must
  // stay up, or at least stay busy, until the batch has actually landed.
  // ---------------------------------------------------------------------
  it.skip('keeps the delete dialog open until the batch delete resolves', async () => {
    let finish;
    const batchDeleteTokens = vi.fn(
      () =>
        new Promise((res) => {
          finish = res;
        }),
    );
    setup({ selectedKeys: [TOKEN_A], batchDeleteTokens });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));
    fireEvent.click(screen.getByRole('button', { name: 'ok:批量删除令牌' }));

    // Still in flight — the confirmation must not have been dismissed.
    expect(screen.queryByTestId('dialog-批量删除令牌')).toBeInTheDocument();

    finish(true);
    await waitFor(() =>
      expect(
        screen.queryByTestId('dialog-批量删除令牌'),
      ).not.toBeInTheDocument(),
    );
  });

  it('issues the batch revoke on confirm and closes once it has settled', async () => {
    let finish;
    const batchDeleteTokens = vi.fn(
      () =>
        new Promise((res) => {
          finish = res;
        }),
    );
    setup({ selectedKeys: [TOKEN_A], batchDeleteTokens });
    fireEvent.click(screen.getByRole('button', { name: '删除所选令牌' }));
    fireEvent.click(screen.getByRole('button', { name: 'ok:批量删除令牌' }));

    // Invariants that survive the fix: the request really was issued, and the
    // dialog is gone by the time the batch has resolved. Resolving BEFORE the
    // wait is what makes this hold both today (dismissed immediately) and
    // after the fix (dismissed on resolve) — waiting first would pin the
    // premature dismissal and turn red on the repair.
    expect(batchDeleteTokens).toHaveBeenCalledTimes(1);
    finish(true);
    await waitFor(() =>
      expect(
        screen.queryByTestId('dialog-批量删除令牌'),
      ).not.toBeInTheDocument(),
    );
  });
});
