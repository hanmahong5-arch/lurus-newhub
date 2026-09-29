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
import { MemoryRouter } from 'react-router-dom';
import HfEmptyState from './HfEmptyState';

describe('HfEmptyState', () => {
  it('renders the title and hint', () => {
    render(
      <HfEmptyState
        testId='es'
        title='No requests in this window'
        hint='Try a wider window, or check back once traffic starts.'
      />,
    );
    const root = screen.getByTestId('es');
    expect(root.textContent).toContain('No requests in this window');
    expect(root.textContent).toContain(
      'Try a wider window, or check back once traffic starts.',
    );
  });

  it('renders action.href as a link to that path', () => {
    render(
      <MemoryRouter>
        <HfEmptyState
          testId='es'
          hint='no traffic yet'
          action={{ label: 'open request logs', href: '/console/v2/log' }}
        />
      </MemoryRouter>,
    );
    const link = screen.getByRole('link', { name: 'open request logs' });
    expect(link.getAttribute('href')).toBe('/console/v2/log');
  });

  it('fires action.onClick when there is no href', () => {
    const onClick = vi.fn();
    render(
      <HfEmptyState
        testId='es'
        hint='no traffic yet'
        action={{ label: 'widen window', onClick }}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: 'widen window' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders without an icon, title or action — hint alone is enough', () => {
    render(<HfEmptyState testId='es' hint='no traffic yet' />);
    expect(screen.getByTestId('es').textContent).toBe('no traffic yet');
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
  });
});
