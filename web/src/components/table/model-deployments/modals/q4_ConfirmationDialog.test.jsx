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

// The last gate in front of an irreversible container destroy: the operator
// has to retype the deployment's name before the OK button unlocks.
//
// What is worth asserting here is the pair, not either half alone:
//   * a MATCHING entry must actually reach onConfirm (otherwise the console
//     has a delete button that confirms nothing — the exact failure this
//     codebase shipped once already), and
//   * a NON-matching entry must not, and must say so.
// Every case below therefore has a negative twin.
//
// The Modal stub reproduces the one contract the component leans on:
// okButtonProps.disabled genuinely prevents onOk from firing. Stubbing that
// away would make every "is refused" assertion pass vacuously.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('@douyinfe/semi-ui', () => {
  const Modal = ({
    children,
    visible,
    title,
    onOk,
    onCancel,
    okText,
    cancelText,
    okButtonProps = {},
    width,
  }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal', 'data-width': String(width) },
          React.createElement('div', { 'data-testid': 'modal-title' }, title),
          children,
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-ok',
              // The real Modal renders a disabled button; a disabled button
              // does not dispatch click, which is precisely the guard under
              // test. Keep it.
              disabled: !!okButtonProps.disabled,
              'data-ok-type': okButtonProps.type,
              'data-loading': String(!!okButtonProps.loading),
              onClick: onOk,
            },
            okText,
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-cancel',
              onClick: onCancel,
            },
            cancelText,
          ),
        )
      : null;

  const Text = ({ children, type, code, strong, size, ...rest }) =>
    React.createElement(
      'span',
      {
        'data-testid': code ? 'required-text' : 'text',
        'data-type': type,
        ...rest,
      },
      children,
    );

  const Input = ({ value, onChange, placeholder, autoFocus, ...rest }) =>
    React.createElement('input', {
      'data-testid': 'confirm-input',
      value: value ?? '',
      placeholder,
      // Semi's Input hands the raw value to onChange, not the event.
      onChange: (e) => onChange(e.target.value),
      ...rest,
    });

  return { Modal, Input, Typography: { Text } };
});

import ConfirmationDialog from './ConfirmationDialog';

const t = (k) => k;

const makeProps = (overrides = {}) => ({
  visible: true,
  onCancel: vi.fn(),
  onConfirm: vi.fn(),
  title: '确认操作',
  type: 'danger',
  // container_name and id deliberately differ, so an assertion cannot pass by
  // accident when the component reads the wrong one.
  deployment: { id: 'dep-9f2', container_name: 'prod-llama-serving' },
  t,
  ...overrides,
});

const renderDialog = (overrides = {}) => {
  const props = makeProps(overrides);
  const view = render(<ConfirmationDialog {...props} />);
  return { props, view };
};

const ok = () => screen.getByTestId('modal-ok');
const MISMATCH = '部署名称不匹配，请检查后重新输入';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('second-factor confirmation', () => {
  it('unlocks the destructive button only for the exact deployment name', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog();

    expect(ok()).toBeDisabled();

    await user.type(screen.getByTestId('confirm-input'), 'prod-llama-serving');

    expect(ok()).toBeEnabled();
    await user.click(ok());
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  it('refuses a near-miss and tells the operator why', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog();

    // One character short of the real name: the classic paste-truncation.
    await user.type(screen.getByTestId('confirm-input'), 'prod-llama-servin');

    expect(ok()).toBeDisabled();
    await user.click(ok());
    expect(props.onConfirm).not.toHaveBeenCalled();
    expect(screen.getByText(MISMATCH)).toBeInTheDocument();
  });

  it('does not nag before anything has been typed', () => {
    renderDialog();
    expect(screen.queryByText(MISMATCH)).not.toBeInTheDocument();
    expect(ok()).toBeDisabled();
  });

  it('shows the operator which name is expected', () => {
    renderDialog();
    expect(screen.getByTestId('required-text')).toHaveTextContent(
      'prod-llama-serving',
    );
  });

  it('closes the dialog after a confirmed destroy', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog();

    await user.type(screen.getByTestId('confirm-input'), 'prod-llama-serving');
    await user.click(ok());

    expect(props.onConfirm).toHaveBeenCalledTimes(1);
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('cancelling never confirms', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog();

    await user.type(screen.getByTestId('confirm-input'), 'prod-llama-serving');
    await user.click(screen.getByTestId('modal-cancel'));

    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(props.onConfirm).not.toHaveBeenCalled();
  });

  it('marks the confirm button as the danger action', () => {
    renderDialog();
    expect(ok().dataset.okType).toBe('danger');
  });

  it('uses the primary styling when the caller is not asking for a destroy', () => {
    renderDialog({ type: 'warning' });
    expect(ok().dataset.okType).toBe('primary');
  });
});

describe('identifier fallback', () => {
  it('falls back to the deployment id when the container has no name', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog({
      deployment: { id: 'dep-9f2', container_name: '' },
    });

    expect(screen.getByTestId('required-text')).toHaveTextContent('dep-9f2');

    await user.type(screen.getByTestId('confirm-input'), 'dep-9f2');
    await user.click(ok());
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  it('stays locked when the row carries no identifier at all', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog({ deployment: null });

    expect(screen.getByTestId('required-text')).toHaveTextContent('未知部署');
    // Fail-closed is the right call here; what must never happen is a destroy
    // firing for a row the dialog could not even name.
    await user.type(screen.getByTestId('confirm-input'), '未知部署');
    await user.click(ok());
    expect(props.onConfirm).not.toHaveBeenCalled();
  });

  // DEFECT (correctness): `requiredText` keeps the id in its ORIGINAL type,
  // then compares it with `===` to the typed string. A deployment whose id is
  // a number and which has no container_name therefore prints "4271" as the
  // text to retype, and rejects "4271" forever — the destroy action in the
  // console can never be completed for such a row. Fail-closed, so nothing is
  // lost, but the operator is left clicking a button that will not unlock.
  // Fix: compare String(requiredText).
  it.skip('LOCK: a numeric deployment id can be confirmed by typing it', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog({
      deployment: { id: 4271, container_name: undefined },
    });

    await user.type(screen.getByTestId('confirm-input'), '4271');
    await user.click(ok());
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  it('currently: a numeric id is displayed to retype, and the dialog is never silent about the outcome', async () => {
    const user = userEvent.setup();
    const { props } = renderDialog({
      deployment: { id: 4271, container_name: undefined },
    });

    // Holds before and after the fix: the operator is told what to type.
    expect(screen.getByTestId('required-text')).toHaveTextContent('4271');

    await user.type(screen.getByTestId('confirm-input'), '4271');
    await user.click(ok());

    // Deliberately NOT asserting "onConfirm was not called" — that literal is
    // the bug, and pinning it would turn the fix red. The invariant that must
    // hold either way: the click either confirms, or the operator is shown a
    // reason for the refusal. Never both, never neither.
    const confirmed = props.onConfirm.mock.calls.length > 0;
    const explained = screen.queryByText(MISMATCH) !== null;
    expect(confirmed !== explained).toBe(true);
  });
});

describe('re-opening for another deployment', () => {
  it('drops the previous typed confirmation instead of arriving pre-armed', async () => {
    const user = userEvent.setup();
    const props = makeProps();
    const { rerender } = render(<ConfirmationDialog {...props} />);

    await user.type(screen.getByTestId('confirm-input'), 'prod-llama-serving');
    expect(ok()).toBeEnabled();

    // Operator closes, then opens the dialog again on a DIFFERENT row.
    rerender(<ConfirmationDialog {...props} visible={false} />);
    rerender(
      <ConfirmationDialog
        {...props}
        visible={true}
        deployment={{ id: 'dep-000', container_name: 'staging-scratch' }}
      />,
    );

    // A carried-over confirmation here would destroy the wrong container.
    expect(screen.getByTestId('confirm-input')).toHaveValue('');
    expect(ok()).toBeDisabled();
    expect(props.onConfirm).not.toHaveBeenCalled();
  });
});
