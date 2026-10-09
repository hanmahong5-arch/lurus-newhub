/*
Copyright (C) 2023-2026 QuantumNous

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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError } from '@/lib/api';

import { OrgKeysBatchPage } from './index';

const { post, statusGet } = vi.hoisted(() => ({
  post: vi.fn(),
  statusGet: vi.fn(),
}));

vi.mock('@/lib/api', async (orig) => {
  const actual = await orig<typeof import('@/lib/api')>();
  return {
    ...actual,
    tenantApi: { get: vi.fn(), post, put: vi.fn(), delete: vi.fn() },
    api: { get: statusGet, post: vi.fn(), put: vi.fn(), delete: vi.fn() },
  };
});

vi.mock('@tanstack/react-router', () => ({
  Link: (p: { to: string; children: ReactNode }) => (
    <a href={p.to}>{p.children}</a>
  ),
}));

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <OrgKeysBatchPage />
    </QueryClientProvider>,
  );
}

function paste(text: string) {
  fireEvent.change(screen.getByLabelText('Roster CSV'), {
    target: { value: text },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  statusGet.mockResolvedValue({
    quota_per_unit: 500000,
    quota_display_type: 'USD',
  });
});

describe('OrgKeysBatchPage', () => {
  it('previews rows, blocks submit on errors, enables on a clean roster', () => {
    renderPage();
    paste('employee_ref,name\nE1,A\nE1,B');
    expect(screen.getByText('Duplicate employee_ref')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Issue keys' })).toBeDisabled();
    paste('employee_ref,name\nE1,A\nE2,B');
    expect(screen.getByTestId('preview-summary')).toHaveTextContent(
      '2 rows ready',
    );
    expect(screen.getByRole('button', { name: 'Issue keys' })).toBeEnabled();
  });

  it('shows a parse error for a missing header', () => {
    renderPage();
    paste('foo\nbar');
    expect(screen.getByTestId('parse-error')).toHaveTextContent('header');
  });

  it('submits the real wire body and shows the one-time result', async () => {
    post.mockResolvedValue({
      created: [
        {
          id: 1,
          name: 'A',
          employee_ref: 'E1',
          project_id: 0,
          key: 'sk-plain',
        },
      ],
      skipped: [{ employee_ref: 'E2', id: 9, name: 'B' }],
      requested: 2,
    });
    renderPage();
    await screen.findByLabelText('Roster CSV');
    // let the status query settle so quota can be converted
    await waitFor(() => expect(statusGet).toHaveBeenCalled());
    paste('employee_ref,name,dept_external_code,quota\nE1,A,D1,1\nE2,B,D1,');
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Issue keys' })).toBeEnabled(),
    );
    await userEvent.click(screen.getByRole('button', { name: 'Issue keys' }));
    expect(await screen.findByTestId('batch-result')).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith('/tokens/batch', {
      tokens: [
        {
          name: 'A',
          employee_ref: 'E1',
          unlimited_quota: false,
          remain_quota: 500000,
        },
        {
          name: 'B',
          employee_ref: 'E2',
          unlimited_quota: true,
          remain_quota: 0,
        },
      ],
    });
    expect(
      screen.getByText('Keys are shown only this once'),
    ).toBeInTheDocument();
    expect(screen.getByText('sk-plain')).toBeInTheDocument();
    expect(screen.getByText(/E2 \(B\)/)).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: /Download keys CSV/ }),
    ).toBeEnabled();
  });

  it('409 payer_not_set shows the hint with a link to members', async () => {
    post.mockRejectedValue(
      new ApiError('no payer', { status: 409, code: 'payer_not_set' }),
    );
    renderPage();
    paste('employee_ref\nE1');
    await userEvent.click(screen.getByRole('button', { name: 'Issue keys' }));
    const box = await screen.findByTestId('payer-missing');
    expect(box).toHaveTextContent(
      'Please set a payer on the Members page first.',
    );
    expect(screen.getByRole('link', { name: 'Go to Members' })).toHaveAttribute(
      'href',
      '/org/members',
    );
  });

  it('other failures show the server message and keep the roster', async () => {
    post.mockRejectedValue(new ApiError('tokens[0]: boom', { status: 400 }));
    renderPage();
    paste('employee_ref\nE1');
    await userEvent.click(screen.getByRole('button', { name: 'Issue keys' }));
    await waitFor(() =>
      expect(screen.getByTestId('submit-error')).toHaveTextContent(
        'tokens[0]: boom',
      ),
    );
    expect(screen.queryByTestId('payer-missing')).toBeNull();
    expect(screen.getByLabelText('Roster CSV')).toHaveValue('employee_ref\nE1');
  });
});
