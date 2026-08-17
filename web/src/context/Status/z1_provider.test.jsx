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
import React, { useContext } from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import { StatusContext, StatusProvider } from './index';

const Probe = () => {
  const [state, dispatch] = useContext(StatusContext);
  return (
    <div>
      <span data-testid='addr'>
        {state.status?.ServerAddress ?? 'unloaded'}
      </span>
      <button
        onClick={() =>
          dispatch({
            type: 'set',
            payload: { ServerAddress: 'https://hub.example.test' },
          })
        }
      >
        set
      </button>
      <button onClick={() => dispatch({ type: 'unset' })}>unset</button>
    </div>
  );
};

describe('StatusProvider', () => {
  it('renders the unloaded state before any status is dispatched', () => {
    render(
      <StatusProvider>
        <Probe />
      </StatusProvider>,
    );
    expect(screen.getByTestId('addr')).toHaveTextContent('unloaded');
  });

  it('propagates set/unset to consumers', async () => {
    const user = userEvent.setup();
    render(
      <StatusProvider>
        <Probe />
      </StatusProvider>,
    );

    await user.click(screen.getByText('set'));
    expect(screen.getByTestId('addr')).toHaveTextContent(
      'https://hub.example.test',
    );

    await user.click(screen.getByText('unset'));
    expect(screen.getByTestId('addr')).toHaveTextContent('unloaded');
  });

  it('exposes the value as a [state, dispatch] tuple', () => {
    let seen = null;
    const Capture = () => {
      seen = useContext(StatusContext);
      return null;
    };
    render(
      <StatusProvider>
        <Capture />
      </StatusProvider>,
    );
    expect(Array.isArray(seen)).toBe(true);
    expect(seen).toHaveLength(2);
    expect(seen[0]).toEqual({ status: undefined });
    expect(typeof seen[1]).toBe('function');
  });

  // DEFECT (see report): same default-value shape mismatch as UserContext.
  // App.jsx does `const [statusState] = useContext(StatusContext)` at its
  // very first line, so rendering App outside a StatusProvider crashes the
  // whole tree rather than degrading to "status not loaded yet".
  // Unskip once createContext is seeded with a [state, dispatch] tuple.
  it.skip('falls back to the unloaded default outside a provider', () => {
    render(<Probe />);
    expect(screen.getByTestId('addr')).toHaveTextContent('unloaded');
  });

  it('currently throws instead of using the createContext default', () => {
    expect(() => render(<Probe />)).toThrow(/not iterable/i);
  });
});
