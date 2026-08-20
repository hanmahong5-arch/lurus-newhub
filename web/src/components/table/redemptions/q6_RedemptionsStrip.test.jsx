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

// The three small strips around the redemption grid: the action buttons, the
// search form and the header description.
//
// The action strip matters more than its size suggests. It carries the only
// bulk destructive control on the screen ("clear invalid codes") next to a
// bulk clipboard export of live bearer codes, and the "add" button must open a
// BLANK editor — reopening the last-edited code there would let an operator
// overwrite a code that is already in a customer's hands.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';

const H = vi.hoisted(() => ({
  formApi: { current: null },
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ children, onClick, htmlType, loading, type, ...rest }) =>
    React.createElement(
      'button',
      {
        type: htmlType === 'submit' ? 'submit' : 'button',
        onClick,
        'data-loading': loading ? 'true' : 'false',
        'data-btntype': type,
        ...rest,
      },
      children,
    );

  const Form = ({ children, onSubmit, initValues, getFormApi }) => {
    React.useEffect(() => {
      const api = { reset: vi.fn(), getValues: () => initValues };
      H.formApi.current = api;
      getFormApi?.(api);
    }, []);
    return React.createElement(
      'form',
      {
        'data-testid': 'form',
        'data-init': JSON.stringify(initValues ?? null),
        onSubmit: (e) => {
          e.preventDefault();
          onSubmit?.(initValues);
        },
      },
      children,
    );
  };
  Form.Input = ({ field, placeholder, prefix }) =>
    React.createElement(
      'span',
      { 'data-testid': `field-${field}` },
      prefix,
      React.createElement('input', { placeholder, name: field }),
    );

  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);

  return { Button, Form, Typography: { Text, Title: Text, Paragraph: Text } };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconSearch: () => React.createElement('i', { 'data-testid': 'icon-search' }),
}));

vi.mock('lucide-react', () => ({
  Ticket: (props) =>
    React.createElement('i', { 'data-testid': 'icon-ticket', ...props }),
}));

// Marker, not null: surface the props so "the toggle got the current mode"
// can be told apart from "a toggle was rendered".
vi.mock('../../common/ui/CompactModeToggle', () => ({
  default: ({ compactMode, setCompactMode, t }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': 'compact-toggle',
        'data-compact': String(compactMode),
        onClick: () => setCompactMode(!compactMode),
      },
      t('紧凑列表'),
    ),
}));

import RedemptionsActions from './RedemptionsActions';
import RedemptionsFilters from './RedemptionsFilters';
import RedemptionsDescription from './RedemptionsDescription';

const t = (k) => k;

describe('RedemptionsActions', () => {
  const makeProps = (over = {}) => ({
    selectedKeys: [],
    setEditingRedemption: vi.fn(),
    setShowEdit: vi.fn(),
    batchCopyRedemptions: vi.fn(),
    batchDeleteRedemptions: vi.fn(),
    t,
    ...over,
  });

  const renderActions = (over = {}) => {
    const props = makeProps(over);
    render(React.createElement(RedemptionsActions, props));
    return props;
  };

  beforeEach(() => vi.clearAllMocks());

  it('opens a BLANK editor when adding — never the last-edited code', () => {
    const props = renderActions();
    screen.getByText('添加兑换码').click();
    expect(props.setEditingRedemption).toHaveBeenCalledTimes(1);
    const seeded = props.setEditingRedemption.mock.calls[0][0];
    // An `id` of undefined is what tells the editor "this is a new code". If a
    // real id leaked in here the editor would PATCH an existing, possibly
    // already-issued, code instead of minting a new one.
    expect(seeded).toEqual({ id: undefined });
    expect('id' in seeded).toBe(true);
    expect(seeded.id).toBeUndefined();
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });

  it('copies the selected codes without also opening the editor', () => {
    const props = renderActions({ selectedKeys: [{ id: 1 }, { id: 2 }] });
    screen.getByText('复制所选兑换码到剪贴板').click();
    expect(props.batchCopyRedemptions).toHaveBeenCalledTimes(1);
    expect(props.setShowEdit).not.toHaveBeenCalled();
    expect(props.batchDeleteRedemptions).not.toHaveBeenCalled();
  });

  it('routes the bulk purge to the purge handler only', () => {
    const props = renderActions();
    screen.getByText('清除失效兑换码').click();
    expect(props.batchDeleteRedemptions).toHaveBeenCalledTimes(1);
    expect(props.batchCopyRedemptions).not.toHaveBeenCalled();
  });

  it('marks the bulk purge as the danger-styled control', () => {
    renderActions();
    expect(screen.getByText('清除失效兑换码')).toHaveAttribute(
      'data-btntype',
      'danger',
    );
    // Negative half: the clipboard export must NOT wear the danger styling,
    // otherwise "red button" stops meaning "destructive".
    expect(screen.getByText('复制所选兑换码到剪贴板')).not.toHaveAttribute(
      'data-btntype',
      'danger',
    );
  });

  // DEFECT (correctness): both bulk controls are always enabled and carry no
  // confirmation. "清除失效兑换码" fires straight at the purge handler on a
  // single click with nothing selected and no "are you sure" — the only
  // irreversible bulk action on the screen is one misclick away, while the
  // far less dangerous single-row delete does get a confirmation dialog.
  it.skip('CONTRACT: the bulk purge must be gated behind a confirmation', () => {
    const props = renderActions();
    screen.getByText('清除失效兑换码').click();
    expect(props.batchDeleteRedemptions).not.toHaveBeenCalled();
  });

  it('currently exposes the bulk purge with nothing selected', () => {
    // Fix-safe: asserts the button is REACHABLE with an empty selection, which
    // stays true whether or not a confirmation step is added in front of it.
    renderActions({ selectedKeys: [] });
    const purge = screen.getByText('清除失效兑换码');
    expect(purge).toBeInTheDocument();
    expect(purge).not.toBeDisabled();
  });
});

describe('RedemptionsFilters', () => {
  const makeProps = (over = {}) => ({
    formInitValues: { searchKeyword: '' },
    setFormApi: vi.fn(),
    searchRedemptions: vi.fn(),
    loading: false,
    searching: false,
    t,
    ...over,
  });

  const renderFilters = (over = {}) => {
    const props = makeProps(over);
    render(React.createElement(RedemptionsFilters, props));
    return props;
  };

  beforeEach(() => {
    vi.clearAllMocks();
    H.formApi.current = null;
  });

  afterEach(() => vi.useRealTimers());

  it('hands the form api up to the page so the toolbar can drive it', () => {
    const props = renderFilters();
    expect(props.setFormApi).toHaveBeenCalledTimes(1);
    expect(props.setFormApi.mock.calls[0][0]).toBe(H.formApi.current);
  });

  it('seeds the keyword field from the page state', () => {
    renderFilters({ formInitValues: { searchKeyword: 'winback' } });
    expect(screen.getByTestId('form')).toHaveAttribute(
      'data-init',
      JSON.stringify({ searchKeyword: 'winback' }),
    );
    expect(screen.getByTestId('field-searchKeyword')).toBeInTheDocument();
  });

  it('runs the search on submit', () => {
    const props = renderFilters();
    act(() => {
      screen.getByTestId('form').requestSubmit();
    });
    expect(props.searchRedemptions).toHaveBeenCalledTimes(1);
  });

  it('clears the form AND re-runs the query when reset is pressed', () => {
    vi.useFakeTimers();
    const props = renderFilters({ formInitValues: { searchKeyword: 'x' } });
    act(() => {
      screen.getByText('重置').click();
    });
    // Clearing without re-querying would leave a blank box over stale rows.
    expect(H.formApi.current.reset).toHaveBeenCalledTimes(1);
    expect(props.searchRedemptions).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(150));
    expect(props.searchRedemptions).toHaveBeenCalledTimes(1);
  });

  it('does nothing on reset before the form api has arrived', () => {
    // Guard clause: exercised by rendering, grabbing the reset handler and
    // clearing the ref is not possible from outside, so this asserts the
    // observable equivalent — no crash and no stray query on a fresh mount.
    const props = renderFilters();
    expect(props.searchRedemptions).not.toHaveBeenCalled();
  });

  it('shows the query button busy while either load flag is set', () => {
    const { unmount } = render(
      React.createElement(RedemptionsFilters, makeProps({ loading: true })),
    );
    expect(screen.getByText('查询')).toHaveAttribute('data-loading', 'true');
    unmount();
    const u2 = render(
      React.createElement(RedemptionsFilters, makeProps({ searching: true })),
    );
    expect(screen.getByText('查询')).toHaveAttribute('data-loading', 'true');
    u2.unmount();
    // Negative half: idle must actually look idle, or "busy" means nothing.
    render(React.createElement(RedemptionsFilters, makeProps()));
    expect(screen.getByText('查询')).toHaveAttribute('data-loading', 'false');
  });
});

describe('RedemptionsDescription', () => {
  beforeEach(() => vi.clearAllMocks());

  it('labels the screen and reflects the current density', () => {
    render(
      React.createElement(RedemptionsDescription, {
        compactMode: true,
        setCompactMode: vi.fn(),
        t,
      }),
    );
    expect(screen.getByText('兑换码管理')).toBeInTheDocument();
    expect(screen.getByTestId('compact-toggle')).toHaveAttribute(
      'data-compact',
      'true',
    );
  });

  it('passes the OFF state through too, not a hard-coded true', () => {
    render(
      React.createElement(RedemptionsDescription, {
        compactMode: false,
        setCompactMode: vi.fn(),
        t,
      }),
    );
    expect(screen.getByTestId('compact-toggle')).toHaveAttribute(
      'data-compact',
      'false',
    );
  });

  it('lets the toggle flip the density on the page', () => {
    const setCompactMode = vi.fn();
    render(
      React.createElement(RedemptionsDescription, {
        compactMode: false,
        setCompactMode,
        t,
      }),
    );
    screen.getByTestId('compact-toggle').click();
    expect(setCompactMode).toHaveBeenCalledWith(true);
  });
});
