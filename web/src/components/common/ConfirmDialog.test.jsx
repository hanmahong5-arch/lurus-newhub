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
import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

// Mirror i18next's en behaviour: a string second argument is the English
// defaultValue, otherwise the key itself is the text; {{var}} interpolation
// either way. ConfirmDialog's own labels are bare-key style ('取消'), the
// primitive underneath uses key + fallback ('console.common.close', 'close').
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split('{{' + k + '}}').join(String(v));
        }
      }
      return out;
    },
    i18n: { language: 'en' },
  }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// No Semi mock on purpose: the component under test must be the real thing
// end to end, dialog surface included. The earlier version of this file
// shimmed Modal/Input/Button, which is how a11y, Escape and backdrop
// behaviour went untested for a year.
import ConfirmDialog from './ConfirmDialog';

const CONFIRM_TEXT = 'production-key';

const baseProps = () => ({
  visible: true,
  title: 'Revoke token "production-key"?',
  consequenceList: ['Will stop working immediately', 'Cannot be undone'],
  confirmText: CONFIRM_TEXT,
  onConfirm: vi.fn(),
  onCancel: vi.fn(),
});

const getInput = () => screen.getByTestId('confirm-dialog-input');
const getConfirmButton = () => screen.getByTestId('confirm-dialog-confirm');
const getCancelButton = () => screen.getByRole('button', { name: '取消' });
const backdrop = () => screen.getByTestId('confirm-dialog-backdrop');

// A backdrop dismissal is mousedown + click on the same node; a click alone
// is what a drag released over the backdrop looks like and must not close.
const clickBackdrop = () => {
  fireEvent.mouseDown(backdrop());
  fireEvent.click(backdrop());
};

afterEach(() => {
  // The dialog primitive locks body scroll while mounted; a test that fails
  // mid-way must not leak the lock into the next one.
  document.body.style.overflow = '';
});

describe('ConfirmDialog', () => {
  // 1. The dialog is a real modal dialog for assistive tech, named by its
  //    title. Every v2 confirm surface inherits this from one place.
  it('renders role=dialog, aria-modal, named by the title', () => {
    render(<ConfirmDialog {...baseProps()} />);
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Revoke token "production-key"?');
  });

  // 2. visible=false mounts nothing — callers keep the component in the
  //    tree permanently and flip `visible`; a hidden dialog must not hold a
  //    scroll lock or an Escape listener.
  it('renders nothing while visible=false', () => {
    render(<ConfirmDialog {...baseProps()} visible={false} />);
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(document.body.style.overflow).not.toBe('hidden');
  });

  // 3. Focus lands in the typed-confirmation input on open so the user can
  //    start typing immediately — half the point of the typed-confirm UX.
  it('moves focus into the input on open', async () => {
    render(<ConfirmDialog {...baseProps()} />);
    await waitFor(() => {
      expect(document.activeElement).toBe(getInput());
    });
  });

  // 4. Typed arm: disabled at mount, disabled on a near miss (strict
  //    case-sensitive equality — no trim, no fuzzy match), enabled on the
  //    exact phrase. The input is a native <input>, so a DOM change event
  //    with e.target.value is the whole contract.
  it('arms confirm only when the typed text exactly equals confirmText', () => {
    render(<ConfirmDialog {...baseProps()} />);
    expect(getConfirmButton()).toBeDisabled();

    fireEvent.change(getInput(), { target: { value: 'PRODUCTION-KEY' } });
    expect(getConfirmButton()).toBeDisabled();

    fireEvent.change(getInput(), { target: { value: 'production-ke' } });
    expect(getConfirmButton()).toBeDisabled();

    fireEvent.change(getInput(), { target: { value: ' production-key' } });
    expect(getConfirmButton()).toBeDisabled();

    fireEvent.change(getInput(), { target: { value: CONFIRM_TEXT } });
    expect(getConfirmButton()).not.toBeDisabled();
  });

  // 5. The consequence list is the part the operator is meant to read
  //    before typing; it must render every line.
  it('renders each consequence line', () => {
    render(<ConfirmDialog {...baseProps()} />);
    const items = screen
      .getByTestId('confirm-dialog-consequences')
      .querySelectorAll('li');
    expect(Array.from(items).map((li) => li.textContent)).toEqual([
      'Will stop working immediately',
      'Cannot be undone',
    ]);
  });

  // 6. Every cancel path → onCancel: the Cancel button, the header X,
  //    Escape and a backdrop click.
  it('Cancel button, X, Escape and backdrop each call onCancel', async () => {
    const props = baseProps();
    render(<ConfirmDialog {...props} />);

    fireEvent.click(getCancelButton());
    expect(props.onCancel).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'close' }));
    expect(props.onCancel).toHaveBeenCalledTimes(2);

    await userEvent.keyboard('{Escape}');
    expect(props.onCancel).toHaveBeenCalledTimes(3);

    clickBackdrop();
    expect(props.onCancel).toHaveBeenCalledTimes(4);
  });

  // 7. Clicking Confirm while armed fires onConfirm exactly once.
  it('Confirm click calls onConfirm once when armed', () => {
    const props = baseProps();
    render(<ConfirmDialog {...props} />);
    fireEvent.change(getInput(), { target: { value: CONFIRM_TEXT } });
    fireEvent.click(getConfirmButton());
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  // 8. Enter in the input confirms only while armed; unarmed Enter is inert
  //    (no accidental submit from a half-typed phrase).
  it('Enter confirms when armed and does nothing otherwise', () => {
    const props = baseProps();
    render(<ConfirmDialog {...props} />);

    fireEvent.change(getInput(), { target: { value: 'production' } });
    fireEvent.keyDown(getInput(), { key: 'Enter' });
    expect(props.onConfirm).not.toHaveBeenCalled();

    fireEvent.change(getInput(), { target: { value: CONFIRM_TEXT } });
    fireEvent.keyDown(getInput(), { key: 'Enter' });
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  // 9. While onConfirm's Promise is pending the dialog is busy: Confirm,
  //    Cancel, the X and the input are disabled, and Escape / backdrop are
  //    refused, so a double click or a reflex Escape can neither fire a
  //    second mutation nor throw the in-flight one away. Resolving releases
  //    it, re-armed for retry (closing on success is the caller's job).
  it('refuses re-fire, Escape and backdrop while pending, then re-arms', async () => {
    let resolveFn;
    const pendingPromise = new Promise((res) => {
      resolveFn = res;
    });
    const props = {
      ...baseProps(),
      onConfirm: vi.fn(() => pendingPromise),
    };
    render(<ConfirmDialog {...props} />);
    fireEvent.change(getInput(), { target: { value: CONFIRM_TEXT } });

    fireEvent.click(getConfirmButton());
    fireEvent.click(getConfirmButton());
    expect(props.onConfirm).toHaveBeenCalledTimes(1);

    expect(getConfirmButton()).toBeDisabled();
    expect(getCancelButton()).toBeDisabled();
    expect(screen.getByRole('button', { name: 'close' })).toBeDisabled();
    expect(getInput()).toBeDisabled();
    expect(screen.getByRole('dialog')).toHaveAttribute('aria-busy', 'true');

    fireEvent.keyDown(document.body, { key: 'Escape' });
    fireEvent.keyDown(getInput(), { key: 'Escape' });
    clickBackdrop();
    expect(props.onCancel).not.toHaveBeenCalled();

    resolveFn();
    await waitFor(() => {
      expect(getConfirmButton()).not.toBeDisabled();
    });
    expect(getCancelButton()).not.toBeDisabled();
  });

  // 10. confirmButtonType maps onto the hifi .btn modifiers; 'danger' is the
  //     default because every original call site was destructive.
  it.each([
    [undefined, 'btn danger'],
    ['danger', 'btn danger'],
    ['warning', 'btn warning'],
    ['primary', 'btn primary'],
  ])('confirmButtonType=%s renders class "%s"', (type, cls) => {
    render(<ConfirmDialog {...baseProps()} confirmButtonType={type} />);
    expect(getConfirmButton().className).toBe(cls);
  });

  // 11. Labels: Cancel and the default Confirm caption come from the
  //     existing i18n keys; confirmButtonText overrides the caption only.
  it('uses the existing i18n keys for Cancel / default Confirm and honours confirmButtonText', () => {
    const { unmount } = render(<ConfirmDialog {...baseProps()} />);
    expect(getCancelButton()).toBeInTheDocument();
    expect(getConfirmButton()).toHaveTextContent('删除');
    expect(screen.getByText('请输入 production-key 以确认操作:')).toBeTruthy();
    unmount();

    render(<ConfirmDialog {...baseProps()} confirmButtonText='purge all' />);
    expect(getConfirmButton()).toHaveTextContent('purge all');
  });

  // 12. Reopening clears the typed phrase. A user who hit Cancel after
  //     typing "alpha" must not find the field still primed when the same
  //     dialog reopens for a DIFFERENT resource.
  it('clears the typed text when the dialog reopens', () => {
    const props = baseProps();
    const { rerender } = render(<ConfirmDialog {...props} />);
    fireEvent.change(getInput(), { target: { value: CONFIRM_TEXT } });
    expect(getConfirmButton()).not.toBeDisabled();

    rerender(<ConfirmDialog {...props} visible={false} />);
    rerender(<ConfirmDialog {...props} visible />);
    expect(getInput().value).toBe('');
    expect(getConfirmButton()).toBeDisabled();
  });
});
