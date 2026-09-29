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
import { fireEvent, render, screen } from '@testing-library/react';
import SessionList from './SessionList';

const baseProps = {
  sessions: [],
  activeSessionId: null,
  messages: [],
  sessionTitle: 'new chat',
  sessionStartedAgo: '0s',
  turnCount: 0,
  deletingSessionId: null,
  onNewChat: () => {},
  onOpenSession: () => {},
  onDeleteSession: () => {},
};

describe('SessionList', () => {
  it('fires onNewChat when + new chat is clicked', () => {
    const onNewChat = vi.fn();
    render(<SessionList {...baseProps} onNewChat={onNewChat} />);
    fireEvent.click(screen.getByText(/\+ new chat/i));
    expect(onNewChat).toHaveBeenCalledTimes(1);
  });

  it('shows the empty-state message when there are no saved sessions', () => {
    render(<SessionList {...baseProps} />);
    expect(screen.getByText(/no saved conversations yet/i)).toBeTruthy();
  });

  it('opens a session on row click', () => {
    const onOpenSession = vi.fn();
    render(
      <SessionList
        {...baseProps}
        sessions={[{ id: 7, title: 'old convo', message_count: 2 }]}
        onOpenSession={onOpenSession}
      />,
    );
    fireEvent.click(screen.getByTestId('session-list-row-7'));
    expect(onOpenSession).toHaveBeenCalledWith(7);
  });

  it('renders the current-session block for an unsaved draft not yet in sessions', () => {
    render(
      <SessionList
        {...baseProps}
        messages={[{ role: 'user', content: 'hi' }]}
        sessionTitle='hi'
      />,
    );
    expect(screen.getByTestId('session-row')).toBeTruthy();
    expect(screen.getByText('hi')).toBeTruthy();
  });

  // Regression: delete used to fire immediately from the row's own button.
  it('delete opens a confirm dialog and does not call onDeleteSession until confirmed', () => {
    const onDeleteSession = vi.fn();
    render(
      <SessionList
        {...baseProps}
        sessions={[{ id: 9, title: 'to delete', message_count: 1 }]}
        onDeleteSession={onDeleteSession}
      />,
    );

    fireEvent.click(screen.getByTestId('session-delete-9'));
    expect(screen.getByTestId('session-delete-confirm-dialog')).toBeTruthy();
    expect(onDeleteSession).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('session-delete-confirm-btn'));
    expect(onDeleteSession).toHaveBeenCalledWith(9);
  });

  it('cancel closes the dialog and never calls onDeleteSession', () => {
    const onDeleteSession = vi.fn();
    render(
      <SessionList
        {...baseProps}
        sessions={[{ id: 9, title: 'to delete', message_count: 1 }]}
        onDeleteSession={onDeleteSession}
      />,
    );

    fireEvent.click(screen.getByTestId('session-delete-9'));
    fireEvent.click(screen.getByTestId('session-delete-cancel-btn'));
    expect(screen.queryByTestId('session-delete-confirm-dialog')).toBeNull();
    expect(onDeleteSession).not.toHaveBeenCalled();
  });

  it('clicking delete does not also open the session (stopPropagation)', () => {
    const onOpenSession = vi.fn();
    render(
      <SessionList
        {...baseProps}
        sessions={[{ id: 9, title: 'to delete', message_count: 1 }]}
        onOpenSession={onOpenSession}
      />,
    );
    fireEvent.click(screen.getByTestId('session-delete-9'));
    expect(onOpenSession).not.toHaveBeenCalled();
  });
});
