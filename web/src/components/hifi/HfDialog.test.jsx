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
import React, { useRef } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import HfDialog, { HfDialogFooter } from './HfDialog';

// Two fields and a submit button: enough to exercise focus order, the trap
// and implicit form submission without a real page around it.
const Body = () => (
  <>
    <input data-testid='first' aria-label='first' />
    <input data-testid='second' aria-label='second' />
    <HfDialogFooter>
      <button type='submit' data-testid='submit' className='btn primary'>
        save
      </button>
    </HfDialogFooter>
  </>
);

describe('HfDialog', () => {
  afterEach(() => {
    document.body.style.overflow = '';
  });

  it('is a labelled modal dialog: aria-labelledby the title, ariaLabel as fallback', () => {
    const { unmount } = render(
      <HfDialog title='New tenant' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('New tenant');
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy();
    unmount();

    render(
      <HfDialog ariaLabel='model detail' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    expect(screen.getByRole('dialog')).toHaveAccessibleName('model detail');
  });

  it('Escape calls onClose once', async () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose}>
        <Body />
      </HfDialog>,
    );
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('Escape is ignored while busy', async () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose} busy>
        <Body />
      </HfDialog>,
    );
    await userEvent.keyboard('{Escape}');
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('with two dialogs stacked, Escape closes only the top one', async () => {
    const closeA = vi.fn();
    const closeB = vi.fn();
    render(
      <HfDialog title='a' onClose={closeA}>
        <Body />
      </HfDialog>,
    );
    render(
      <HfDialog title='b' onClose={closeB}>
        <Body />
      </HfDialog>,
    );
    await userEvent.keyboard('{Escape}');
    // The StatsDrawer test fires on window directly; that path must also
    // respect the stack.
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(closeB).toHaveBeenCalledTimes(2);
    expect(closeA).not.toHaveBeenCalled();
  });

  it('Escape from a node outside the panel (a portaled modal) is ignored', () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose}>
        <Body />
      </HfDialog>,
    );
    const outside = document.createElement('input');
    document.body.appendChild(outside);
    fireEvent.keyDown(outside, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    // body itself is fine (no element focused).
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    outside.remove();
  });

  it('moves focus in on open: initialFocusRef first, else the first focusable', () => {
    const WithRef = () => {
      const ref = useRef(null);
      return (
        <HfDialog title='t' onClose={() => {}} initialFocusRef={ref}>
          <input data-testid='first' />
          <input data-testid='second' ref={ref} />
        </HfDialog>
      );
    };
    const { unmount } = render(<WithRef />);
    expect(document.activeElement).toBe(screen.getByTestId('second'));
    unmount();

    render(
      <HfDialog title='t' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    expect(document.activeElement).toBe(screen.getByTestId('first'));
  });

  it('returns focus to the opener on unmount', () => {
    render(<button data-testid='opener'>open</button>);
    const opener = screen.getByTestId('opener');
    opener.focus();
    const { unmount } = render(
      <HfDialog title='t' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    expect(document.activeElement).not.toBe(opener);
    unmount();
    expect(document.activeElement).toBe(opener);
  });

  it('does not throw or focus when the opener was detached before unmount', () => {
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();
    const focus = vi.spyOn(opener, 'focus');
    const { unmount } = render(
      <HfDialog title='t' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    opener.remove();
    expect(() => unmount()).not.toThrow();
    expect(focus).not.toHaveBeenCalled();
  });

  it('traps Tab: last wraps to first, Shift+Tab from first wraps to last', async () => {
    render(
      <HfDialog title='t' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    const user = userEvent.setup();
    const close = screen.getByRole('button', { name: 'close' });
    const submit = screen.getByTestId('submit');
    submit.focus();
    await user.tab();
    expect(document.activeElement).toBe(close);
    await user.tab({ shift: true });
    expect(document.activeElement).toBe(submit);
  });

  it('mousedown + click on the backdrop closes', async () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose} backdropTestId='bd'>
        <Body />
      </HfDialog>,
    );
    await userEvent.click(screen.getByTestId('bd'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('a click inside the panel does not close', async () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose} testId='panel'>
        <Body />
      </HfDialog>,
    );
    await userEvent.click(screen.getByTestId('panel'));
    await userEvent.click(screen.getByTestId('first'));
    expect(onClose).not.toHaveBeenCalled();
  });

  it('a drag that starts inside the panel and releases on the backdrop does not close', () => {
    const onClose = vi.fn();
    render(
      <HfDialog title='t' onClose={onClose} backdropTestId='bd'>
        <Body />
      </HfDialog>,
    );
    fireEvent.mouseDown(screen.getByTestId('first'));
    fireEvent.click(screen.getByTestId('bd'));
    expect(onClose).not.toHaveBeenCalled();
    // A later clean click on the backdrop still closes.
    fireEvent.mouseDown(screen.getByTestId('bd'));
    fireEvent.click(screen.getByTestId('bd'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('the X button is named "close" and is disabled while busy', async () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <HfDialog title='t' onClose={onClose}>
        <Body />
      </HfDialog>,
    );
    const x = screen.getByRole('button', { name: 'close' });
    await userEvent.click(x);
    expect(onClose).toHaveBeenCalledTimes(1);

    rerender(
      <HfDialog title='t' onClose={onClose} busy>
        <Body />
      </HfDialog>,
    );
    expect(screen.getByRole('button', { name: 'close' })).toBeDisabled();
  });

  it('locks body scroll while any dialog is open and restores after the last one closes', () => {
    document.body.style.overflow = 'auto';
    const a = render(
      <HfDialog title='a' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    const b = render(
      <HfDialog title='b' onClose={() => {}}>
        <Body />
      </HfDialog>,
    );
    expect(document.body.style.overflow).toBe('hidden');
    a.unmount();
    expect(document.body.style.overflow).toBe('hidden');
    b.unmount();
    expect(document.body.style.overflow).toBe('auto');
  });

  it("as='form' submits on Enter in a field and on the submit button", async () => {
    const onSubmit = vi.fn((e) => e.preventDefault());
    render(
      <HfDialog title='t' onClose={() => {}} as='form' onSubmit={onSubmit}>
        <Body />
      </HfDialog>,
    );
    expect(screen.getByRole('dialog').tagName).toBe('FORM');
    const user = userEvent.setup();
    await user.type(screen.getByTestId('first'), 'x{Enter}');
    expect(onSubmit).toHaveBeenCalledTimes(1);
    await user.click(screen.getByTestId('submit'));
    expect(onSubmit).toHaveBeenCalledTimes(2);
  });

  it("variant='side' renders the drawer classes, wide when asked", () => {
    const { unmount } = render(
      <HfDialog ariaLabel='m' onClose={() => {}} variant='side' testId='p'>
        <Body />
      </HfDialog>,
    );
    expect(screen.getByTestId('p').className).toBe('hf-drawer');
    expect(screen.getByTestId('p').tagName).toBe('ASIDE');
    unmount();

    render(
      <HfDialog ariaLabel='m' onClose={() => {}} variant='side' wide testId='p'>
        <Body />
      </HfDialog>,
    );
    expect(screen.getByTestId('p').className).toBe('hf-drawer hf-drawer-wide');
  });
});
