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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import HfUserMenu, { isMacPlatform } from './HfUserMenu';

const user = { username: 'shell-test', display_name: 'Shell T.' };

const renderMenu = (props = {}) =>
  render(
    <MemoryRouter>
      <HfUserMenu user={user} onLogout={vi.fn()} {...props} />
    </MemoryRouter>,
  );

beforeEach(() => {
  window.localStorage.clear();
});

describe('HfUserMenu', () => {
  it('renders the login button when there is no bridged user', () => {
    render(
      <MemoryRouter>
        <HfUserMenu user={null} onLogout={vi.fn()} />
      </MemoryRouter>,
    );
    expect(screen.getByText('login')).toBeInTheDocument();
    expect(screen.queryByTestId('shell-user-menu-trigger')).toBeNull();
  });

  it('opens a role=menu with Settings and Logout when the trigger is clicked', () => {
    renderMenu();
    expect(screen.queryByRole('menu')).toBeNull();

    fireEvent.click(screen.getByTestId('shell-user-menu-trigger'));

    const menu = screen.getByRole('menu');
    expect(menu).toBeInTheDocument();
    const settingsLink = screen.getByText('Settings').closest('a');
    expect(settingsLink.getAttribute('href')).toBe('/console/v2/settings');
    expect(screen.getByText('logout')).toBeInTheDocument();
  });

  it('closes on Escape and returns focus to the trigger button', () => {
    renderMenu();
    const trigger = screen.getByTestId('shell-user-menu-trigger');
    fireEvent.click(trigger);
    expect(screen.getByRole('menu')).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });

    expect(screen.queryByRole('menu')).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it('calls onLogout when the logout menu item is clicked', () => {
    const onLogout = vi.fn();
    renderMenu({ onLogout });
    fireEvent.click(screen.getByTestId('shell-user-menu-trigger'));
    fireEvent.click(screen.getByText('logout'));
    expect(onLogout).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('moves focus between items with the arrow keys', () => {
    renderMenu();
    fireEvent.click(screen.getByTestId('shell-user-menu-trigger'));
    const settingsItem = screen.getByText('Settings').closest('a');
    const logoutItem = screen.getByText('logout');
    expect(document.activeElement).toBe(settingsItem);

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(logoutItem);

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' });
    expect(document.activeElement).toBe(settingsItem);

    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowUp' });
    expect(document.activeElement).toBe(logoutItem);
  });

  it('closes when clicking outside', () => {
    renderMenu();
    fireEvent.click(screen.getByTestId('shell-user-menu-trigger'));
    expect(screen.getByRole('menu')).toBeInTheDocument();

    fireEvent.mouseDown(document.body);

    expect(screen.queryByRole('menu')).toBeNull();
  });

  it('still renders the language and theme ghost buttons', () => {
    renderMenu();
    // Theme glyph — default light theme shows the "switch to dark" state.
    expect(screen.getByTitle('switch to dark')).toBeInTheDocument();
    // Language toggle shows the target language label.
    expect(screen.getByTitle('switch language')).toBeInTheDocument();
  });
});

describe('isMacPlatform', () => {
  it('is a pure function of navigator.platform/userAgent', () => {
    // jsdom's default navigator has no "Mac" token.
    expect(typeof isMacPlatform()).toBe('boolean');
  });
});
