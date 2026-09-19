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
import { render, screen, waitFor } from '@testing-library/react';

// The icon pack resolves only when this test says so, which is what makes the
// "what does the first paint look like" assertions meaningful: everything
// before `release()` is the state a real visitor sees while the async chunk
// is still in flight.
//
// `pack.attempts` counts how many times the chunk was actually fetched, which
// is what separates "memoised" from "re-issued per icon" and what proves the
// retry after a failure really happened.
const { gate, pack } = vi.hoisted(() => {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return {
    gate: { promise, release: () => release() },
    pack: { attempts: 0 },
  };
});

vi.mock('@lobehub/icons', async () => {
  pack.attempts += 1;
  if (pack.attempts === 1) {
    // The first fetch fails — the ordinary way this happens in production is a
    // deploy rotating the hashed chunk names underneath an already-open tab,
    // so the request for the icon chunk 404s.
    throw new Error(
      'Failed to fetch dynamically imported module: /assets/icons-es-OLDHASH.js',
    );
  }
  await gate.promise;
  const react = await import('react');
  const make = (testid) => (props) =>
    react.createElement('svg', {
      'data-testid': testid,
      'data-size': props.size,
      'data-shape': props.shape,
    });
  const OpenAI = make('openai-icon');
  OpenAI.Color = make('openai-color-icon');
  return { OpenAI };
});

// Semi's barrel needs canvas (lottie-web) under jsdom; the stand-in surfaces
// the placeholder avatar that must be on screen during the first paint.
vi.mock('@douyinfe/semi-ui', () => ({
  Avatar: ({ children, size }) =>
    React.createElement(
      'span',
      { 'data-testid': 'avatar', 'data-size': size },
      children,
    ),
}));

import { getLobeHubIcon, LobeHubIcon } from './lobeIcon';

afterEach(() => {
  vi.restoreAllMocks();
});

// These run in file order and share one module instance of ./lobeIcon, which is
// the point: the load memo, the retry and the warn-once flag are all module
// state, and the only honest way to exercise them is in sequence.
describe('getLobeHubIcon with an asynchronously loaded icon pack', () => {
  it('survives a failed chunk fetch: placeholder stays, and it says so once', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    render(<div>{getLobeHubIcon('OpenAI', 20)}</div>);
    expect(screen.getByTestId('avatar')).toHaveTextContent('O');

    await waitFor(() => expect(warn).toHaveBeenCalledTimes(1));
    expect(warn.mock.calls[0][0]).toMatch(/icon pack failed to load/);

    // Not a blank space and not a thrown render: the page keeps the same
    // initial-letter stand-in an unknown icon name has always produced.
    expect(screen.getByTestId('avatar')).toHaveTextContent('O');
    expect(screen.queryByTestId('openai-icon')).toBeNull();
    expect(pack.attempts).toBe(1);
  });

  it('paints the initial-letter placeholder before the pack arrives, then swaps in the icon', async () => {
    render(<div>{getLobeHubIcon('OpenAI', 20)}</div>);

    // Synchronous first paint: no icon pack yet, so the placeholder stands in.
    expect(screen.getByTestId('avatar')).toHaveTextContent('O');
    expect(screen.queryByTestId('openai-icon')).toBeNull();

    gate.release();

    const icon = await screen.findByTestId('openai-icon');
    expect(icon).toHaveAttribute('data-size', '20');
    expect(screen.queryByTestId('avatar')).toBeNull();

    // ...and this mount is also the retry: the failure above cleared the memo,
    // so a later mount re-issued the import instead of leaving the console
    // pinned to initial letters for the rest of the session.
    expect(pack.attempts).toBe(2);
  });

  it('renders straight from the memoised module on a later mount, with no placeholder frame', async () => {
    // Proves the dynamic import is memoised at module scope rather than being
    // re-issued (and re-awaited) per icon: the pack was already resolved by
    // the test above, so this mount has nothing to wait for.
    render(
      <div>{getLobeHubIcon("OpenAI.Color.shape={'square'}.size=28")}</div>,
    );

    const icon = screen.getByTestId('openai-color-icon');
    expect(icon).toHaveAttribute('data-size', '28');
    expect(icon).toHaveAttribute('data-shape', 'square');
    expect(screen.queryByTestId('avatar')).toBeNull();
  });

  it('keeps the placeholder for a name the pack does not carry', () => {
    render(<div>{getLobeHubIcon('NoSuchVendor')}</div>);
    expect(screen.getByTestId('avatar')).toHaveTextContent('N');
  });

  it('exposes the component form used by the channel and category tables', () => {
    render(<LobeHubIcon name='OpenAI' sub='Color' size={14} />);
    expect(screen.getByTestId('openai-color-icon')).toHaveAttribute(
      'data-size',
      '14',
    );
  });
});
