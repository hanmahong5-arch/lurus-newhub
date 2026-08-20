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

// Vendor-type tab strip. The counts on these tabs are how an operator decides
// where to look, so a tab showing the wrong count — or a tab appearing for a
// vendor with no channels — is a real (if quiet) defect. CHANNEL_OPTIONS is
// the REAL constant here: the mapping from type id to vendor label is exactly
// what the tabs are for.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('@douyinfe/semi-ui', () => {
  const Tabs = ({ activeKey, onChange, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'tabs', 'data-active': activeKey },
      React.Children.map(children, (child) =>
        child
          ? React.createElement(
              'button',
              {
                type: 'button',
                'data-testid': `tab-${child.props.itemKey}`,
                onClick: () => onChange(child.props.itemKey),
              },
              child.props.tab,
            )
          : null,
      ),
    );
  const TabPane = ({ tab }) => React.createElement('span', null, tab);
  return {
    Tabs,
    TabPane,
    Tag: ({ children, color }) =>
      React.createElement(
        'span',
        { 'data-testid': 'count', 'data-color': color },
        children,
      ),
  };
});

vi.mock('../../../helpers', () => ({
  getChannelIcon: (type) =>
    React.createElement('i', {
      'data-testid': 'vendor-icon',
      'data-type': String(type),
    }),
}));

import ChannelsTabs from './ChannelsTabs';

const t = (k) => k;

const makeProps = (over = {}) => ({
  t,
  enableTagMode: false,
  activeTypeKey: 'all',
  setActiveTypeKey: vi.fn(),
  channelTypeCounts: { all: 12, 1: 5, 14: 7 },
  availableTypeKeys: ['1', '14'],
  loadChannels: vi.fn(),
  activePage: 4,
  pageSize: 20,
  idSort: true,
  setActivePage: vi.fn(),
  ...over,
});

const renderTabs = (over = {}) => {
  const props = makeProps(over);
  const view = render(<ChannelsTabs {...props} />);
  return { props, view };
};

beforeEach(() => vi.clearAllMocks());

describe('which tabs exist', () => {
  it('shows "all" plus exactly the vendor types that have channels', () => {
    renderTabs();
    expect(screen.getByTestId('tab-all')).toBeInTheDocument();
    expect(screen.getByTestId('tab-1')).toBeInTheDocument();
    expect(screen.getByTestId('tab-14')).toBeInTheDocument();
    // Vendors with no channels must not clutter the strip.
    expect(screen.queryByTestId('tab-2')).toBeNull();
    expect(screen.queryByTestId('tab-40')).toBeNull();
  });

  it('renders nothing at all in tag-aggregation mode', () => {
    const { view } = renderTabs({ enableTagMode: true });
    expect(view.container).toBeEmptyDOMElement();
  });

  it('degrades to just the "all" tab when no vendor keys are available', () => {
    renderTabs({ availableTypeKeys: [] });
    expect(screen.getByTestId('tab-all')).toBeInTheDocument();
    expect(screen.queryByTestId('tab-1')).toBeNull();
  });
});

describe('tab counts', () => {
  it('prints each vendor count from channelTypeCounts', () => {
    renderTabs();
    expect(screen.getByTestId('tab-all').textContent).toContain('12');
    expect(screen.getByTestId('tab-1').textContent).toContain('5');
    expect(screen.getByTestId('tab-14').textContent).toContain('7');
  });

  it('prints 0 rather than blank or undefined for a type with no count entry', () => {
    renderTabs({ channelTypeCounts: {}, availableTypeKeys: ['1'] });
    expect(screen.getByTestId('tab-all').textContent).toContain('0');
    expect(screen.getByTestId('tab-1').textContent).toContain('0');
    expect(screen.getByTestId('tab-1').textContent).not.toContain('undefined');
  });

  it('highlights the active tab and only the active tab', () => {
    renderTabs({ activeTypeKey: '1' });
    const colors = screen.getAllByTestId('count').map((n) => n.dataset.color);
    // Exactly one red badge — the selected vendor.
    expect(colors.filter((c) => c === 'red')).toHaveLength(1);
    expect(
      screen.getByTestId('tab-1').querySelector('[data-color="red"]'),
    ).not.toBeNull();
  });
});

describe('switching tabs', () => {
  it('resets to page 1 and reloads with the newly-clicked type', async () => {
    const user = userEvent.setup();
    const { props } = renderTabs({ activePage: 4 });

    await user.click(screen.getByTestId('tab-14'));

    expect(props.setActiveTypeKey).toHaveBeenCalledWith('14');
    // Staying on page 4 of the previous vendor's list would show an empty
    // table for a vendor that has 7 channels.
    expect(props.setActivePage).toHaveBeenCalledWith(1);
    expect(props.loadChannels).toHaveBeenCalledWith(1, 20, true, false, '14');
  });

  it('carries the current sort and tag-mode into the reload', async () => {
    const user = userEvent.setup();
    const { props } = renderTabs({ idSort: false, pageSize: 50 });
    await user.click(screen.getByTestId('tab-all'));
    expect(props.loadChannels).toHaveBeenCalledWith(1, 50, false, false, 'all');
  });

  it('labels each vendor tab with its own icon', () => {
    renderTabs();
    const types = screen
      .getAllByTestId('vendor-icon')
      .map((n) => n.dataset.type);
    expect(types).toEqual(['1', '14']);
  });
});
