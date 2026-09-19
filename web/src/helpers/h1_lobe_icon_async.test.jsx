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
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

// The icon pack resolves only when this test says so, which is what makes the
// "what does the first paint look like" assertions meaningful: everything
// before `release()` is the state a real visitor sees while the async chunk
// is still in flight.
const { gate } = vi.hoisted(() => {
  let release;
  const promise = new Promise((resolve) => {
    release = resolve;
  });
  return { gate: { promise, release: () => release() } };
});

vi.mock('@lobehub/icons', async () => {
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

describe('getLobeHubIcon with an asynchronously loaded icon pack', () => {
  it('paints the initial-letter placeholder before the pack arrives, then swaps in the icon', async () => {
    render(<div>{getLobeHubIcon('OpenAI', 20)}</div>);

    // Synchronous first paint: no icon pack yet, so the placeholder stands in.
    expect(screen.getByTestId('avatar')).toHaveTextContent('O');
    expect(screen.queryByTestId('openai-icon')).toBeNull();

    gate.release();

    const icon = await screen.findByTestId('openai-icon');
    expect(icon).toHaveAttribute('data-size', '20');
    expect(screen.queryByTestId('avatar')).toBeNull();
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
