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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, act } from '@testing-library/react';

const stub = vi.hoisted(() => ({
  formApi: null,
  formProps: null,
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;

  const Form = ({ getFormApi, onSubmit, children, ...rest }) => {
    stub.formProps = rest;
    React.useEffect(() => {
      getFormApi?.(stub.formApi);
    }, [getFormApi]);
    return h(
      'form',
      {
        'data-testid': 'filters-form',
        onSubmit: (e) => {
          e.preventDefault();
          onSubmit?.({ from: 'submit' });
        },
      },
      children,
    );
  };
  Form.Input = ({ field, placeholder }) =>
    h('input', { name: field, placeholder, readOnly: true });

  return {
    Form,
    Button: ({ children, onClick, htmlType, loading }) =>
      h(
        'button',
        {
          type: htmlType === 'submit' ? 'submit' : 'button',
          onClick,
          'data-loading': String(!!loading),
        },
        children,
      ),
  };
});

vi.mock('@douyinfe/semi-icons', () => {
  const h = React.createElement;
  return { IconSearch: () => h('i', { 'data-testid': 'icon-search' }) };
});

import TokensFilters from './TokensFilters';

const t = (k) => k;

let props;
const setup = (over = {}) => {
  props = {
    formInitValues: { searchKeyword: '', searchToken: '' },
    setFormApi: vi.fn(),
    searchTokens: vi.fn(),
    loading: false,
    searching: false,
    t,
    ...over,
  };
  return render(<TokensFilters {...props} />);
};

beforeEach(() => {
  stub.formApi = { reset: vi.fn() };
  stub.formProps = null;
});

afterEach(() => {
  vi.useRealTimers();
});

describe('TokensFilters', () => {
  it('exposes both search fields with distinct placeholders', () => {
    setup();
    expect(screen.getByPlaceholderText('搜索关键字')).toHaveAttribute(
      'name',
      'searchKeyword',
    );
    // The secret-search field is separate — searching a key by keyword would
    // not find it.
    expect(screen.getByPlaceholderText('密钥')).toHaveAttribute(
      'name',
      'searchToken',
    );
  });

  it('hands the form api up to the page so it can read the filters', () => {
    setup();
    expect(props.setFormApi).toHaveBeenCalledWith(stub.formApi);
  });

  it('seeds the form from formInitValues and lets empty filters through', () => {
    setup();
    expect(stub.formProps.initValues).toBe(props.formInitValues);
    // allowEmpty is what makes "clear the box and search" mean "no filter"
    // instead of a validation error.
    expect(stub.formProps.allowEmpty).toBe(true);
  });

  it('submitting runs the search', () => {
    setup();
    fireEvent.submit(screen.getByTestId('filters-form'));
    expect(props.searchTokens).toHaveBeenCalledTimes(1);
  });

  it('the 查询 button submits the form rather than calling search directly', () => {
    setup();
    fireEvent.click(screen.getByRole('button', { name: '查询' }));
    expect(props.searchTokens).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['loading', { loading: true, searching: false }],
    ['searching', { loading: false, searching: true }],
  ])('shows the query button busy while %s', (_label, flags) => {
    setup(flags);
    expect(screen.getByRole('button', { name: '查询' })).toHaveAttribute(
      'data-loading',
      'true',
    );
  });

  it('is not busy when neither flag is set', () => {
    setup();
    expect(screen.getByRole('button', { name: '查询' })).toHaveAttribute(
      'data-loading',
      'false',
    );
  });

  it('reset clears the form and re-runs the search once the reset settles', () => {
    vi.useFakeTimers();
    setup();

    fireEvent.click(screen.getByRole('button', { name: '重置' }));

    expect(stub.formApi.reset).toHaveBeenCalledTimes(1);
    // Deliberately deferred: reading the form back immediately would still see
    // the old values.
    expect(props.searchTokens).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(100);
    });
    expect(props.searchTokens).toHaveBeenCalledTimes(1);
  });

  it('reset before the form api arrives is a no-op, not a crash', () => {
    // getFormApi never fires with a usable api in this configuration.
    stub.formApi = undefined;
    vi.useFakeTimers();
    setup();

    expect(() =>
      fireEvent.click(screen.getByRole('button', { name: '重置' })),
    ).not.toThrow();
    act(() => {
      vi.advanceTimersByTime(200);
    });
    expect(props.searchTokens).not.toHaveBeenCalled();
  });
});
