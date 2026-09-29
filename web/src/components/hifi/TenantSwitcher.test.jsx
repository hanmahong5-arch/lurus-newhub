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
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import TenantSwitcher from './TenantSwitcher';

const tenants = [
  { id: 'acme', name: 'acme · prod', mode: 'Reseller' },
  { id: 'beta', name: 'beta · stage', mode: 'EndUser' },
];

describe('TenantSwitcher — real-data path (Lane B 2026-05-18)', () => {
  it('renders provided tenantName + mode without demo placeholders', () => {
    render(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
      />,
    );
    // Trigger shows the active tenant name.
    expect(screen.getByText('acme · prod')).toBeInTheDocument();
    // Mode pill renders the mode label uppercased — find "Reseller" specifically.
    expect(screen.getAllByText(/reseller/i).length).toBeGreaterThanOrEqual(1);
    // Demo "personal workspace" placeholder must not leak through.
    expect(screen.queryByText(/personal workspace/i)).toBeNull();
  });

  it('handles empty tenants list without crashing or showing demo data', () => {
    render(<TenantSwitcher tenants={[]} tenantName='' mode='Personal' />);
    // No "acme" leak from removed DEMO_TENANTS.
    expect(screen.queryByText(/acme/i)).toBeNull();
    expect(screen.queryByText(/contoso/i)).toBeNull();
  });

  it('opens dropdown on trigger click and lists tenants by name', () => {
    render(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
      />,
    );
    fireEvent.click(screen.getByRole('button'));

    // Both tenant names visible in the dropdown options.
    expect(screen.getAllByText('acme · prod').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('beta · stage')).toBeInTheDocument();
  });

  it('fires onSelect with tenant id when an option is clicked', () => {
    const onSelect = vi.fn();
    render(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
        onSelect={onSelect}
      />,
    );
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByText('beta · stage'));
    expect(onSelect).toHaveBeenCalledWith('beta', 'EndUser');
  });

  it('syncs to updated tenantName prop after first paint', () => {
    const { rerender } = render(
      <TenantSwitcher tenants={[]} tenantName='' mode='Personal' />,
    );
    // Simulate caller (HFShell) finishing /api/v2/admin/tenants fetch.
    rerender(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
      />,
    );
    expect(screen.getByText('acme · prod')).toBeInTheDocument();
  });
});

// There is no server-side tenant/mode switch to wire the three mode buttons
// to (see the comment above HFShell's TenantSwitcher call site) — without an
// onSelect handler, they must render as real disabled controls, not merely
// look inert.
describe('TenantSwitcher — mode switch requires onSelect', () => {
  it('renders the mode buttons disabled and leaves the mode label unchanged on click, when no onSelect is passed', () => {
    render(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
      />,
    );
    fireEvent.click(screen.getByRole('button'));

    const personalOption = screen.getByText('Personal').closest('button');
    expect(personalOption).toBeDisabled();

    fireEvent.click(personalOption);
    // Mode pill still shows the original mode (Reseller) — the click did
    // nothing.
    expect(screen.getAllByText(/reseller/i).length).toBeGreaterThanOrEqual(1);
  });

  it('still fires onSelect(null, mode) for the mode buttons when onSelect is passed', () => {
    const onSelect = vi.fn();
    render(
      <TenantSwitcher
        tenants={tenants}
        tenantName='acme · prod'
        mode='Reseller'
        onSelect={onSelect}
      />,
    );
    fireEvent.click(screen.getByRole('button'));

    const personalOption = screen.getByText('Personal').closest('button');
    expect(personalOption).not.toBeDisabled();
    fireEvent.click(personalOption);

    expect(onSelect).toHaveBeenCalledWith(null, 'Personal');
  });
});
