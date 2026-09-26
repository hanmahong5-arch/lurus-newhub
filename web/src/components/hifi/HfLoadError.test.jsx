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
import { act, fireEvent, render, screen } from '@testing-library/react';
import HfLoadError from './HfLoadError';

describe('HfLoadError', () => {
  it('is announced as an alert and keeps the caller test hooks', () => {
    render(
      <HfLoadError testId='x-error' retryTestId='x-retry' onRetry={() => {}} />,
    );
    const panel = screen.getByTestId('x-error');
    expect(panel.getAttribute('role')).toBe('alert');
    expect(screen.getByTestId('x-retry')).toBeTruthy();
  });

  it('says what the reader can do next for each failure shape', () => {
    const { rerender } = render(<HfLoadError testId='p' />);
    expect(screen.getByTestId('p').textContent).toContain('Try again');

    rerender(<HfLoadError testId='p' status='unauthenticated' />);
    expect(screen.getByTestId('p').textContent).toContain(
      'session has expired',
    );
    expect(screen.getByTestId('p').textContent).toContain('Sign in again');

    rerender(<HfLoadError testId='p' status='forbidden' />);
    expect(screen.getByTestId('p').textContent).toContain(
      'Ask an administrator',
    );
  });

  it('prefers the page title over the generic headline', () => {
    render(<HfLoadError testId='p' title='Couldn’t load projects' />);
    expect(screen.getByTestId('p').textContent).toContain(
      'Couldn’t load projects',
    );
    expect(screen.getByTestId('p').textContent).not.toContain(
      'Couldn’t load this data',
    );
  });

  it('renders no button when there is nothing to retry', () => {
    render(<HfLoadError testId='p' />);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('goes busy while the retry is in flight, so a double click fires once', async () => {
    let resolve;
    const onRetry = vi.fn(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    render(<HfLoadError retryTestId='r' onRetry={onRetry} />);
    const btn = screen.getByTestId('r');

    fireEvent.click(btn);
    fireEvent.click(btn);
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute('aria-busy')).toBe('true');
    expect(btn.textContent).toContain('Retrying');

    await act(async () => {
      resolve();
    });
    expect(btn.disabled).toBe(false);
    expect(btn.textContent).not.toContain('Retrying');
  });

  it('comes back out of busy when the retry itself throws', async () => {
    const onRetry = vi.fn(() => Promise.reject(new Error('still down')));
    render(<HfLoadError retryTestId='r' onRetry={onRetry} />);
    const btn = screen.getByTestId('r');
    await act(async () => {
      fireEvent.click(btn);
    });
    expect(btn.disabled).toBe(false);
  });
});
