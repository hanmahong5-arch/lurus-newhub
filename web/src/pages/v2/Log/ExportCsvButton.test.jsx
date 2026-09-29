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
import { describe, it, expect, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

import ExportCsvButton from './ExportCsvButton';

// window.location.href is not navigable in jsdom (it throws "Not
// implemented: navigation"), so replace it with a plain writable object —
// this file only asserts the string ExportCsvButton assigns, not that jsdom
// actually navigates.
let hrefSink;
beforeEach(() => {
  hrefSink = { value: '' };
  delete window.location;
  window.location = {
    get href() {
      return hrefSink.value;
    },
    set href(v) {
      hrefSink.value = v;
    },
  };
});

describe('ExportCsvButton', () => {
  it('builds the export URL with no filters set', () => {
    render(<ExportCsvButton tenantSlug='acme' />);
    fireEvent.click(screen.getByTestId('log-export-btn'));
    expect(hrefSink.value).toBe('/api/v2/acme/logs/export');
  });

  it('carries model_name and token_name through unchanged', () => {
    render(
      <ExportCsvButton
        tenantSlug='acme'
        filterModel='model-a'
        filterToken='prod-key'
      />,
    );
    fireEvent.click(screen.getByTestId('log-export-btn'));
    const url = new URL(hrefSink.value, 'http://localhost');
    expect(url.pathname).toBe('/api/v2/acme/logs/export');
    expect(url.searchParams.get('model_name')).toBe('model-a');
    expect(url.searchParams.get('token_name')).toBe('prod-key');
  });

  it('converts start/end datetime-local values to unix seconds, matching the trace table filters', () => {
    render(
      <ExportCsvButton
        tenantSlug='acme'
        filterStart='2026-09-01T00:00'
        filterEnd='2026-09-02T00:00'
      />,
    );
    fireEvent.click(screen.getByTestId('log-export-btn'));
    const url = new URL(hrefSink.value, 'http://localhost');
    expect(url.searchParams.get('start_time')).toBe(
      String(Math.floor(new Date('2026-09-01T00:00').getTime() / 1000)),
    );
    expect(url.searchParams.get('end_time')).toBe(
      String(Math.floor(new Date('2026-09-02T00:00').getTime() / 1000)),
    );
  });

  it('renders no emoji — a real icon takes its place', () => {
    render(<ExportCsvButton tenantSlug='acme' />);
    expect(screen.getByTestId('log-export-btn').textContent).not.toContain(
      '📥',
    );
  });
});
