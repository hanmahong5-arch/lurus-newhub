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

import { UserContext, UserProvider } from './index';

const ADMIN = { id: 7, username: 'alice', role: 100 };
const MEMBER = { id: 7, username: 'alice', role: 1 };

// Consumer that mirrors how the app reads the context: array destructuring.
const Probe = () => {
  const [state, dispatch] = useContext(UserContext);
  return (
    <div>
      <span data-testid='who'>{state.user?.username ?? 'anonymous'}</span>
      <span data-testid='admin'>{state.user?.role === 100 ? 'yes' : 'no'}</span>
      <button onClick={() => dispatch({ type: 'login', payload: ADMIN })}>
        login-admin
      </button>
      <button onClick={() => dispatch({ type: 'login', payload: MEMBER })}>
        demote
      </button>
      <button onClick={() => dispatch({ type: 'logout' })}>logout</button>
    </div>
  );
};

describe('UserProvider', () => {
  it('starts anonymous and non-admin', () => {
    render(
      <UserProvider>
        <Probe />
      </UserProvider>,
    );
    expect(screen.getByTestId('who')).toHaveTextContent('anonymous');
    expect(screen.getByTestId('admin')).toHaveTextContent('no');
  });

  it('login then logout round-trips the identity through consumers', async () => {
    const user = userEvent.setup();
    render(
      <UserProvider>
        <Probe />
      </UserProvider>,
    );

    await user.click(screen.getByText('login-admin'));
    expect(screen.getByTestId('who')).toHaveTextContent('alice');
    expect(screen.getByTestId('admin')).toHaveTextContent('yes');

    await user.click(screen.getByText('logout'));
    expect(screen.getByTestId('who')).toHaveTextContent('anonymous');
    expect(screen.getByTestId('admin')).toHaveTextContent('no');
  });

  // Privilege safety end-to-end: a re-login carrying a lower role must be
  // observable by consumers immediately — no stale admin chrome.
  it('a re-login with a lower role revokes admin in consumers', async () => {
    const user = userEvent.setup();
    render(
      <UserProvider>
        <Probe />
      </UserProvider>,
    );

    await user.click(screen.getByText('login-admin'));
    expect(screen.getByTestId('admin')).toHaveTextContent('yes');

    await user.click(screen.getByText('demote'));
    expect(screen.getByTestId('who')).toHaveTextContent('alice');
    expect(screen.getByTestId('admin')).toHaveTextContent('no');
  });

  it('exposes the value as a [state, dispatch] tuple', () => {
    let seen = null;
    const Capture = () => {
      seen = useContext(UserContext);
      return null;
    };
    render(
      <UserProvider>
        <Capture />
      </UserProvider>,
    );
    expect(Array.isArray(seen)).toBe(true);
    expect(seen).toHaveLength(2);
    expect(seen[0]).toEqual({ user: undefined });
    expect(typeof seen[1]).toBe('function');
  });

  // DEFECT (see report): React.createContext() is seeded with the OBJECT
  // `{ state, dispatch }` while UserProvider supplies the ARRAY
  // `[state, dispatch]`. Any consumer rendered outside a UserProvider does
  // `const [state, dispatch] = useContext(UserContext)` and crashes with
  // "is not iterable" instead of falling back to the anonymous default the
  // createContext argument was clearly written to provide.
  // Expected (correct) contract asserted below; unskip once the default
  // value is changed to the same tuple shape as the provider value.
  it.skip('falls back to the anonymous default outside a provider', () => {
    render(<Probe />);
    expect(screen.getByTestId('who')).toHaveTextContent('anonymous');
  });

  // The observable consequence of the defect above, pinned so the skipped
  // test above cannot be "fixed" by weakening it.
  it('currently throws instead of using the createContext default', () => {
    expect(() => render(<Probe />)).toThrow(/not iterable/i);
  });
});
