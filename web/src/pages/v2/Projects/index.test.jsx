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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

// Interactive stub. The real ConfirmDialog latches `pending` while onConfirm's
// promise is in flight, so it already blocks a double-click; this stub does NOT
// (deliberately) — the page's own guard is what these tests exercise.
vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm, onCancel, title }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-ok', onClick: onConfirm },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

import HFProjects from './index';
import { API } from '../../../helpers';

const project = (o = {}) => ({
  id: 3,
  name: 'Marketing',
  description: 'brand spend',
  deleted: false,
  ...o,
});

// The page issues two GETs against the same mock — branch on the URL.
const wireGet = ({ projects = [], spend = [], spendFails = false } = {}) => {
  API.get.mockImplementation((url) => {
    if (String(url).endsWith('/projects/spend')) {
      return spendFails
        ? Promise.reject(new Error('boom'))
        : Promise.resolve({ data: { success: true, data: { items: spend } } });
    }
    return Promise.resolve({
      data: { success: true, data: { items: projects } },
    });
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  API.delete.mockReset();
  try {
    localStorage.setItem('tenant_slug', 'acme');
  } catch (_) {}
});

describe('Projects page', () => {
  it('lists the tenant projects', async () => {
    wireGet({ projects: [project()] });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-row-3'));
    expect(screen.getByText('Marketing')).toBeTruthy();
    expect(screen.getByText('brand spend')).toBeTruthy();
  });

  it('asks for retired projects so a delete can be undone later', async () => {
    wireGet({ projects: [project()] });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-row-3'));
    const listCall = API.get.mock.calls.find(
      ([url]) => !String(url).includes('/spend'),
    );
    expect(listCall[0]).toContain('include_deleted=1');
  });

  it('shows the empty state when the tenant has no projects', async () => {
    wireGet({});

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-empty'));
  });

  it('creates a project via POST with a trimmed name', async () => {
    wireGet({});
    API.post.mockResolvedValue({ data: { success: true, data: project() } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-new-btn'));

    fireEvent.click(screen.getByTestId('proj-new-btn'));
    await waitFor(() => screen.getByTestId('proj-save'));

    fireEvent.change(screen.getByTestId('proj-name'), {
      target: { value: '  Research  ' },
    });
    fireEvent.change(screen.getByTestId('proj-description'), {
      target: { value: 'R&D spend' },
    });
    fireEvent.click(screen.getByTestId('proj-save'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith('/api/v2/acme/projects', {
        name: 'Research',
        description: 'R&D spend',
      });
    });
  });

  it('edits an existing project via PUT (the name IS editable)', async () => {
    wireGet({ projects: [project()] });
    API.put.mockResolvedValue({ data: { success: true, data: project() } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-edit-btn-3'));

    fireEvent.click(screen.getByTestId('proj-edit-btn-3'));
    await waitFor(() => screen.getByTestId('proj-save'));

    // Unlike the model-limits modal, renaming is allowed: log rows reference
    // the numeric id, so a rename re-labels history rather than orphaning it.
    expect(screen.getByTestId('proj-name').disabled).toBeFalsy();

    fireEvent.change(screen.getByTestId('proj-name'), {
      target: { value: 'Growth' },
    });
    fireEvent.click(screen.getByTestId('proj-save'));

    await waitFor(() => {
      expect(API.put).toHaveBeenCalledWith(
        '/api/v2/acme/projects/3',
        expect.objectContaining({ name: 'Growth' }),
      );
    });
  });

  it('deletes behind the typed-confirm dialog', async () => {
    wireGet({ projects: [project()] });
    API.delete.mockResolvedValue({
      data: { success: true, data: { detached_token_ids: [] } },
    });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-delete-btn-3'));

    fireEvent.click(screen.getByTestId('proj-delete-btn-3'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith('/api/v2/acme/projects/3');
    });
  });

  it('renders the unassigned bucket and a total that equals the rows', async () => {
    // The invariant the page exists to convey: per-project spend sums to the
    // tenant total, which is only true when project_id 0 is shown as a row.
    wireGet({
      projects: [project()],
      spend: [
        {
          project_id: 3,
          name: 'Marketing',
          unassigned: false,
          count: 2,
          total_quota: 1000000,
        },
        {
          project_id: 0,
          name: '',
          unassigned: true,
          count: 1,
          total_quota: 500000,
        },
      ],
    });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-spend-table'));
    expect(screen.getByTestId('proj-spend-row-3')).toBeTruthy();
    expect(screen.getByTestId('proj-spend-row-0')).toBeTruthy();
    expect(screen.getByText('unassigned')).toBeTruthy();
    // 1000000 + 500000 quota units = $3.00 at 500000 units/$.
    const total = screen.getByTestId('proj-spend-total');
    expect(total.textContent).toContain('$3.00');
  });

  it('renders a retired project id that no longer resolves to a name', async () => {
    wireGet({
      projects: [],
      spend: [
        {
          project_id: 17,
          name: '',
          unassigned: false,
          count: 1,
          total_quota: 0,
        },
      ],
    });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-spend-row-17'));
    // Falls back to the id rather than rendering a blank cell, so the row is
    // still identifiable — the spend must never silently vanish.
    expect(screen.getByText('#17')).toBeTruthy();
  });

  it('shows the spend empty state without breaking the list', async () => {
    wireGet({ projects: [project()], spendFails: true });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-row-3'));
    expect(screen.getByTestId('proj-spend-empty')).toBeTruthy();
  });

  it('shows the forbidden panel on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(<HFProjects />);

    await waitFor(() => screen.getByTestId('proj-forbidden'));
  });
});

describe('Projects page — reversibility', () => {
  it('offers a one-click undo carrying the detached token ids', async () => {
    wireGet({ projects: [project()] });
    API.delete.mockResolvedValue({
      data: { success: true, data: { detached_token_ids: [11, 12] } },
    });
    API.post.mockResolvedValue({ data: { success: true } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-delete-btn-3'));

    fireEvent.click(screen.getByTestId('proj-delete-btn-3'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => screen.getByTestId('proj-undo-banner'));
    fireEvent.click(screen.getByTestId('proj-undo-btn'));

    await waitFor(() => {
      // The ids must ride along, or the tokens stay unassigned and the "undo"
      // is only half an undo.
      expect(API.post).toHaveBeenCalledWith('/api/v2/acme/projects/3/restore', {
        reattach_token_ids: [11, 12],
      });
    });
  });

  it('lets the undo banner be dismissed without restoring', async () => {
    wireGet({ projects: [project()] });
    API.delete.mockResolvedValue({
      data: { success: true, data: { detached_token_ids: [] } },
    });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-delete-btn-3'));
    fireEvent.click(screen.getByTestId('proj-delete-btn-3'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => screen.getByTestId('proj-undo-banner'));
    fireEvent.click(screen.getByTestId('proj-undo-dismiss'));

    await waitFor(() => {
      expect(screen.queryByTestId('proj-undo-banner')).toBeNull();
    });
    expect(API.post).not.toHaveBeenCalled();
  });

  it('lists retired projects with a restore button long after the banner is gone', async () => {
    wireGet({
      projects: [
        project(),
        project({ id: 4, name: 'Research', deleted: true }),
      ],
    });
    API.post.mockResolvedValue({ data: { success: true } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-deleted-panel'));

    // A retired project must not pollute the live list or the count.
    expect(screen.queryByTestId('proj-row-4')).toBeNull();
    expect(screen.getByTestId('proj-deleted-row-4')).toBeTruthy();

    fireEvent.click(screen.getByTestId('proj-restore-btn-4'));
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith('/api/v2/acme/projects/4/restore', {
        reattach_token_ids: [],
      });
    });
  });

  it('hides the retired panel when nothing is retired', async () => {
    wireGet({ projects: [project()] });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-row-3'));
    expect(screen.queryByTestId('proj-deleted-panel')).toBeNull();
  });
});

describe('Projects page — repeat-safety', () => {
  // A deferred promise lets a request stay in flight while the test clicks
  // again, which is exactly the double-click the guards exist for.
  const deferred = () => {
    let resolve;
    const promise = new Promise((r) => {
      resolve = r;
    });
    return { promise, resolve };
  };

  it('fires one POST when save is clicked twice', async () => {
    wireGet({});
    const d = deferred();
    API.post.mockReturnValue(d.promise);

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-new-btn'));
    fireEvent.click(screen.getByTestId('proj-new-btn'));
    await waitFor(() => screen.getByTestId('proj-save'));

    fireEvent.change(screen.getByTestId('proj-name'), {
      target: { value: 'Research' },
    });
    const save = screen.getByTestId('proj-save');
    fireEvent.click(save);
    fireEvent.click(save);
    fireEvent.click(save);

    d.resolve({ data: { success: true, data: project() } });
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
  });

  it('fires one DELETE when the confirmation is clicked twice', async () => {
    wireGet({ projects: [project()] });
    const d = deferred();
    API.delete.mockReturnValue(d.promise);

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-delete-btn-3'));
    fireEvent.click(screen.getByTestId('proj-delete-btn-3'));
    await waitFor(() => screen.getByTestId('confirm-dialog'));

    const ok = screen.getByTestId('confirm-ok');
    fireEvent.click(ok);
    fireEvent.click(ok);

    d.resolve({ data: { success: true, data: { detached_token_ids: [] } } });
    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledTimes(1);
    });
  });

  it('fires one restore when undo is clicked twice', async () => {
    wireGet({
      projects: [project({ id: 4, name: 'Research', deleted: true })],
    });
    const d = deferred();
    API.post.mockReturnValue(d.promise);

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-restore-btn-4'));

    const btn = screen.getByTestId('proj-restore-btn-4');
    fireEvent.click(btn);
    fireEvent.click(btn);

    d.resolve({ data: { success: true } });
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
  });

  it('re-enables the form when the save fails, so the user can retry', async () => {
    wireGet({});
    API.post.mockRejectedValue({ response: { status: 409 } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-new-btn'));
    fireEvent.click(screen.getByTestId('proj-new-btn'));
    await waitFor(() => screen.getByTestId('proj-save'));

    fireEvent.change(screen.getByTestId('proj-name'), {
      target: { value: 'Marketing' },
    });
    fireEvent.click(screen.getByTestId('proj-save'));

    // A failed save must leave the modal open and armed — a stuck disabled
    // button is a dead end the user cannot escape without a page reload.
    await waitFor(() => {
      expect(screen.getByTestId('proj-save').disabled).toBe(false);
    });
    fireEvent.click(screen.getByTestId('proj-save'));
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(2);
    });
  });

  it('re-enables restore when it fails, so a name clash can be resolved and retried', async () => {
    wireGet({
      projects: [project({ id: 4, name: 'Research', deleted: true })],
    });
    API.post.mockRejectedValue({ response: { status: 409 } });

    render(<HFProjects />);
    await waitFor(() => screen.getByTestId('proj-restore-btn-4'));

    fireEvent.click(screen.getByTestId('proj-restore-btn-4'));
    await waitFor(() => {
      expect(screen.getByTestId('proj-restore-btn-4').disabled).toBe(false);
    });
    fireEvent.click(screen.getByTestId('proj-restore-btn-4'));
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(2);
    });
  });
});
