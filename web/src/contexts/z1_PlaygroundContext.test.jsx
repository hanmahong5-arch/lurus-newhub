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
import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, renderHook, screen } from '@testing-library/react';

import PlaygroundContext, {
  usePlayground,
  PlaygroundProvider,
} from './PlaygroundContext';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('usePlayground without a provider', () => {
  it('returns the documented no-op fallback shape', () => {
    const { result } = renderHook(() => usePlayground());
    expect(result.current.imageUrls).toEqual([]);
    expect(result.current.imageEnabled).toBe(false);
    expect(typeof result.current.onPasteImage).toBe('function');
  });

  // The fallback onPasteImage swallows the image and only warns. Callers
  // such as CustomInputRender do `if (onPasteImage) onPasteImage(base64)`
  // and then report success to the user, so the drop is silent from the
  // UI's point of view — hence the warn is the only observable signal.
  it('the fallback onPasteImage warns and drops the payload', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { result } = renderHook(() => usePlayground());

    const returned = result.current.onPasteImage('data:image/png;base64,AAA');

    expect(returned).toBeUndefined();
    expect(warn).toHaveBeenCalledTimes(1);
    expect(warn.mock.calls[0][0]).toBe('PlaygroundContext not provided');
  });

  // DEFECT (see report): the fallback object is rebuilt on every call, so
  // `onPasteImage` gets a fresh identity on each render. CustomInputRender
  // feeds it into `useCallback(..., [onPasteImage, ...])` whose result is
  // the dependency of a `useEffect` that add/removeEventListener's 'paste'
  // on the container — so the listener is torn down and re-attached on
  // every single render. Unskip once the fallback is hoisted to a module
  // constant (or memoised).
  it.skip('returns a stable fallback identity across renders', () => {
    const { result, rerender } = renderHook(() => usePlayground());
    const first = result.current.onPasteImage;
    rerender();
    expect(result.current.onPasteImage).toBe(first);
  });

  it('currently hands out a new fallback object on every render', () => {
    const { result, rerender } = renderHook(() => usePlayground());
    const first = result.current;
    rerender();
    expect(result.current).not.toBe(first);
    expect(result.current.onPasteImage).not.toBe(first.onPasteImage);
  });
});

describe('PlaygroundProvider', () => {
  it('hands the exact provided value to consumers', () => {
    const onPasteImage = vi.fn();
    const value = {
      onPasteImage,
      imageUrls: ['https://cdn.example.test/a.png'],
      imageEnabled: true,
    };
    const { result } = renderHook(() => usePlayground(), {
      wrapper: ({ children }) => (
        <PlaygroundProvider value={value}>{children}</PlaygroundProvider>
      ),
    });

    expect(result.current).toBe(value);
    result.current.onPasteImage('data:image/png;base64,AAA');
    expect(onPasteImage).toHaveBeenCalledWith('data:image/png;base64,AAA');
  });

  it('renders its children', () => {
    render(
      <PlaygroundProvider value={{ imageEnabled: true }}>
        <span data-testid='child'>inside</span>
      </PlaygroundProvider>,
    );
    expect(screen.getByTestId('child')).toHaveTextContent('inside');
  });

  it('a null value falls through to the no-op fallback', () => {
    const { result } = renderHook(() => usePlayground(), {
      wrapper: ({ children }) => (
        <PlaygroundProvider value={null}>{children}</PlaygroundProvider>
      ),
    });
    expect(result.current.imageEnabled).toBe(false);
    expect(result.current.imageUrls).toEqual([]);
  });

  it('a provider mounted without a value prop also falls back', () => {
    const { result } = renderHook(() => usePlayground(), {
      wrapper: ({ children }) => (
        <PlaygroundProvider>{children}</PlaygroundProvider>
      ),
    });
    expect(result.current.imageEnabled).toBe(false);
  });

  it('the default export is the same context the hook reads', () => {
    // Behavioural, not existential: consuming the default export directly with
    // a hand-rolled Provider must reach usePlayground. `toBeTruthy()` on the
    // export would pass even if the module exported a second, unrelated context
    // -- which is exactly the mistake that silently disconnects a provider.
    const { result } = renderHook(() => usePlayground(), {
      wrapper: ({ children }) => (
        <PlaygroundContext.Provider
          value={{ imageEnabled: true, imageUrls: ['a.png'] }}
        >
          {children}
        </PlaygroundContext.Provider>
      ),
    });

    expect(result.current.imageEnabled).toBe(true);
    expect(result.current.imageUrls).toEqual(['a.png']);
  });
});
