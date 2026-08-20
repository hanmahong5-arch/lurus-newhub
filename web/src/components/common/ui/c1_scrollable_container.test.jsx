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
import React, { createRef } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act, fireEvent, render } from '@testing-library/react';

import ScrollableContainer from './ScrollableContainer';

// ScrollableContainer's whole job is arithmetic over three DOM numbers
// (scrollTop / clientHeight / scrollHeight). jsdom has no layout engine, so all
// three are hard-wired to 0 and the setter for scrollTop is a no-op. Redefine
// them per-node so the component has real geometry to reason about; everything
// asserted below is the component's own math, not jsdom's.
const stubGeometry = (el, { scrollHeight, clientHeight, scrollTop = 0 }) => {
  Object.defineProperty(el, 'scrollHeight', {
    value: scrollHeight,
    configurable: true,
  });
  Object.defineProperty(el, 'clientHeight', {
    value: clientHeight,
    configurable: true,
  });
  let top = scrollTop;
  Object.defineProperty(el, 'scrollTop', {
    get: () => top,
    set: (v) => {
      top = v;
    },
    configurable: true,
  });
};

const pane = (container) => container.querySelector('.card-content-scroll');
const fade = (container) =>
  container.querySelector('.card-content-fade-indicator');

// Default checkInterval is 100ms and the scroll handler is debounced by 16ms.
const DEFAULT_CHECK_INTERVAL = 100;
const DEBOUNCE = 16;

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ScrollableContainer — rendering contract', () => {
  it('renders children in the scroll pane and applies maxHeight to it', () => {
    const { container } = render(
      <ScrollableContainer maxHeight='12rem'>
        <p>inner content</p>
      </ScrollableContainer>,
    );

    const scroller = pane(container);
    expect(scroller).not.toBeNull();
    expect(scroller.textContent).toBe('inner content');
    // maxHeight lands on the scrolling element, not the outer wrapper —
    // putting it on the wrapper would clip without ever scrolling.
    expect(scroller.style.maxHeight).toBe('12rem');
    expect(container.firstChild.style.maxHeight).toBe('');
  });

  it('merges caller class names and forwards unknown props to the wrapper', () => {
    const { container } = render(
      <ScrollableContainer
        className='my-wrapper'
        contentClassName='my-content'
        fadeIndicatorClassName='my-fade'
        data-testid='sc'
        id='sc-id'
      >
        x
      </ScrollableContainer>,
    );

    const wrapper = container.firstChild;
    expect(wrapper.className).toBe('card-content-container my-wrapper');
    expect(wrapper.getAttribute('data-testid')).toBe('sc');
    expect(wrapper.getAttribute('id')).toBe('sc-id');
    expect(pane(container).className).toContain('my-content');
    expect(fade(container).className).toContain('my-fade');
  });
});

describe('ScrollableContainer — scroll-state math', () => {
  it('reports non-scrollable and hides the fade when content fits', () => {
    const onScrollStateChange = vi.fn();
    const { container } = render(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        short
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), { scrollHeight: 100, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));

    expect(onScrollStateChange).toHaveBeenCalledTimes(1);
    expect(onScrollStateChange).toHaveBeenCalledWith({
      isScrollable: false,
      isAtBottom: true,
      showScrollHint: false,
      scrollTop: 0,
      scrollHeight: 100,
      clientHeight: 100,
    });
    expect(fade(container).style.opacity).toBe('0');
  });

  it('shows the fade indicator when overflowing and parked at the top', () => {
    const onScrollStateChange = vi.fn();
    const { container } = render(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        long
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), { scrollHeight: 500, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));

    expect(onScrollStateChange).toHaveBeenCalledWith(
      expect.objectContaining({
        isScrollable: true,
        isAtBottom: false,
        showScrollHint: true,
      }),
    );
    expect(fade(container).style.opacity).toBe('1');
  });

  it('hides the fade once scrolled within scrollThreshold of the bottom', () => {
    const { container } = render(
      <ScrollableContainer>long</ScrollableContainer>,
    );
    const scroller = pane(container);
    // 500 tall in a 100 window; default threshold is 5px.
    stubGeometry(scroller, {
      scrollHeight: 500,
      clientHeight: 100,
      scrollTop: 0,
    });
    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));
    expect(fade(container).style.opacity).toBe('1');

    // 394 + 100 = 494 < 495 → still 6px shy of the threshold, keep the hint.
    scroller.scrollTop = 394;
    fireEvent.scroll(scroller);
    act(() => vi.advanceTimersByTime(DEBOUNCE));
    expect(fade(container).style.opacity).toBe('1');

    // 396 + 100 = 496 >= 495 → inside the threshold, hint goes away.
    scroller.scrollTop = 396;
    fireEvent.scroll(scroller);
    act(() => vi.advanceTimersByTime(DEBOUNCE));
    expect(fade(container).style.opacity).toBe('0');
  });

  it('honours a custom scrollThreshold', () => {
    const onScrollStateChange = vi.fn();
    const { container } = render(
      <ScrollableContainer
        scrollThreshold={120}
        onScrollStateChange={onScrollStateChange}
      >
        long
      </ScrollableContainer>,
    );
    // 300 + 100 = 400; bottom-120 = 380 → counts as "at bottom" only because
    // the threshold was widened to 120.
    stubGeometry(pane(container), {
      scrollHeight: 500,
      clientHeight: 100,
      scrollTop: 300,
    });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));

    expect(onScrollStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ isAtBottom: true, showScrollHint: false }),
    );
  });

  it('waits the caller-supplied checkInterval before the first measurement', () => {
    const onScrollStateChange = vi.fn();
    const { container } = render(
      <ScrollableContainer
        checkInterval={400}
        onScrollStateChange={onScrollStateChange}
      >
        x
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), { scrollHeight: 500, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(399));
    expect(onScrollStateChange).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(1));
    expect(onScrollStateChange).toHaveBeenCalledTimes(1);
  });
});

describe('ScrollableContainer — scroll events', () => {
  it('forwards the raw scroll event to onScroll on every scroll', () => {
    const onScroll = vi.fn();
    const { container } = render(
      <ScrollableContainer onScroll={onScroll}>x</ScrollableContainer>,
    );
    const scroller = pane(container);
    stubGeometry(scroller, { scrollHeight: 500, clientHeight: 100 });

    fireEvent.scroll(scroller);
    fireEvent.scroll(scroller);

    // onScroll is NOT debounced — the caller sees every event.
    expect(onScroll).toHaveBeenCalledTimes(2);
    expect(onScroll.mock.calls[0][0].target).toBe(scroller);
  });

  it('debounces the state recompute so a burst of scrolls measures once', () => {
    const onScrollStateChange = vi.fn();
    const { container } = render(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        x
      </ScrollableContainer>,
    );
    const scroller = pane(container);
    stubGeometry(scroller, { scrollHeight: 500, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));
    onScrollStateChange.mockClear();

    fireEvent.scroll(scroller);
    act(() => vi.advanceTimersByTime(10));
    fireEvent.scroll(scroller);
    act(() => vi.advanceTimersByTime(10));
    fireEvent.scroll(scroller);
    // Nothing has fired yet: each event reset the 16ms window.
    expect(onScrollStateChange).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(DEBOUNCE));
    expect(onScrollStateChange).toHaveBeenCalledTimes(1);
  });

  it('drops the pending debounced measurement when unmounted mid-burst', () => {
    const onScrollStateChange = vi.fn();
    const { container, unmount } = render(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        x
      </ScrollableContainer>,
    );
    const scroller = pane(container);
    stubGeometry(scroller, { scrollHeight: 500, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));
    onScrollStateChange.mockClear();

    fireEvent.scroll(scroller);
    unmount();
    act(() => vi.advanceTimersByTime(1000));

    expect(onScrollStateChange).not.toHaveBeenCalled();
  });
});

describe('ScrollableContainer — imperative handle', () => {
  it('getScrollInfo reports live geometry and derived flags', () => {
    const ref = createRef();
    const { container } = render(
      <ScrollableContainer ref={ref} scrollThreshold={50}>
        x
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), {
      scrollHeight: 500,
      clientHeight: 100,
      scrollTop: 360,
    });

    // 360 + 100 = 460 >= 500 - 50 → at bottom.
    expect(ref.current.getScrollInfo()).toEqual({
      scrollTop: 360,
      scrollHeight: 500,
      clientHeight: 100,
      isScrollable: true,
      isAtBottom: true,
    });
  });

  it('getScrollInfo returns null after unmount instead of throwing', () => {
    const ref = createRef();
    const { unmount } = render(
      <ScrollableContainer ref={ref}>x</ScrollableContainer>,
    );
    const handle = ref.current;
    unmount();

    expect(handle.getScrollInfo()).toBeNull();
  });

  it('scrollToTop and scrollToBottom move the pane', () => {
    const ref = createRef();
    const { container } = render(
      <ScrollableContainer ref={ref}>x</ScrollableContainer>,
    );
    const scroller = pane(container);
    stubGeometry(scroller, {
      scrollHeight: 500,
      clientHeight: 100,
      scrollTop: 200,
    });

    act(() => ref.current.scrollToBottom());
    expect(scroller.scrollTop).toBe(500);

    act(() => ref.current.scrollToTop());
    expect(scroller.scrollTop).toBe(0);
  });

  it('checkScrollable() measures immediately, bypassing the debounce', () => {
    const onScrollStateChange = vi.fn();
    const ref = createRef();
    const { container } = render(
      <ScrollableContainer ref={ref} onScrollStateChange={onScrollStateChange}>
        x
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), {
      scrollHeight: 500,
      clientHeight: 100,
      scrollTop: 0,
    });

    act(() => ref.current.checkScrollable());

    expect(onScrollStateChange).toHaveBeenCalledTimes(1);
    expect(onScrollStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ showScrollHint: true }),
    );
  });

  it('checkScrollable() is a no-op (not a crash) once unmounted', () => {
    const ref = createRef();
    const onScrollStateChange = vi.fn();
    const { unmount } = render(
      <ScrollableContainer ref={ref} onScrollStateChange={onScrollStateChange}>
        x
      </ScrollableContainer>,
    );
    const handle = ref.current;
    unmount();

    expect(() => handle.checkScrollable()).not.toThrow();
    expect(onScrollStateChange).not.toHaveBeenCalled();
  });
});

describe('ScrollableContainer — observer fallback', () => {
  it('re-measures via MutationObserver when ResizeObserver is unavailable', async () => {
    // This environment has no ResizeObserver, which is exactly the branch the
    // component guards with `typeof ResizeObserver === 'undefined'`.
    expect(typeof globalThis.ResizeObserver).toBe('undefined');

    const onScrollStateChange = vi.fn();
    const { container, rerender } = render(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        <p>one line</p>
      </ScrollableContainer>,
    );
    stubGeometry(pane(container), { scrollHeight: 500, clientHeight: 100 });

    act(() => vi.advanceTimersByTime(DEFAULT_CHECK_INTERVAL));
    onScrollStateChange.mockClear();

    rerender(
      <ScrollableContainer onScrollStateChange={onScrollStateChange}>
        <p>one line</p>
        <p>a second line grew the content</p>
      </ScrollableContainer>,
    );

    // MutationObserver delivers on a microtask; then the 16ms debounce runs.
    await act(async () => {
      await Promise.resolve();
      vi.advanceTimersByTime(DEBOUNCE);
    });

    expect(onScrollStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ isScrollable: true, showScrollHint: true }),
    );
  });
});
