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
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
// Side-effect import: real i18next instance with en.json resources — see
// the same note in HfActivityChart.test.jsx.
import '../../../i18n/i18n';
import LivePanel from './LivePanel';

const baseProps = {
  loading: false,
  logsStatus: 'ok',
  hasRealtimeData: false,
  qps: 0,
  p50: null,
  p95: null,
  p99: null,
  errorRate: 0,
};

describe('LivePanel — folds when there is nothing to show', () => {
  it('renders one folded line, not three "—" cards, once settled with no data', () => {
    render(<LivePanel {...baseProps} />);
    expect(screen.getByTestId('live-panel-folded')).toBeTruthy();
    expect(screen.queryAllByText('—')).toHaveLength(0);
    expect(screen.getByText('no traffic in last 5 min')).toBeTruthy();
  });

  it('does NOT fold while the fetch is still in flight — shows loading cards instead', () => {
    render(<LivePanel {...baseProps} loading hasRealtimeData={false} />);
    expect(screen.queryByTestId('live-panel-folded')).toBeNull();
    expect(screen.getByText('qps')).toBeTruthy();
    expect(screen.getAllByText('…').length).toBeGreaterThan(0);
  });

  it('renders all three cards when there is real data — no folding', () => {
    render(
      <LivePanel
        {...baseProps}
        hasRealtimeData
        qps={3}
        p50={100}
        p95={150}
        p99={200}
        errorRate={0.01}
      />,
    );
    expect(screen.queryByTestId('live-panel-folded')).toBeNull();
    expect(screen.getByText('qps')).toBeTruthy();
    expect(screen.getByText('latency · ms')).toBeTruthy();
    expect(screen.getByText('error rate')).toBeTruthy();
    expect(screen.getByText('3.0')).toBeTruthy();
    expect(screen.getByText('100ms')).toBeTruthy();
    expect(screen.getByText('1.0%')).toBeTruthy();
  });

  it('folds with the unable-to-load caption, not the idle one, when the fetch failed', () => {
    render(<LivePanel {...baseProps} logsStatus='error' />);
    const folded = screen.getByTestId('live-panel-folded');
    expect(folded.textContent).toBe('unable to load');
    expect(screen.queryByText('no traffic in last 5 min')).toBeNull();
  });

  it('renders the live title with the pulsing dot', () => {
    const { container } = render(<LivePanel {...baseProps} />);
    expect(screen.getByText('live · last 5 min')).toBeTruthy();
    expect(container.querySelector('.live-dot')).toBeTruthy();
  });
});
