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
import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';

// Semi's Icon wrapper just hosts the svg it is handed; render it straight
// through so the artwork each logo hands over is inspectable.
vi.mock('@douyinfe/semi-ui', () => ({
  Icon: ({ svg }) => React.createElement('i', { 'data-testid': 'icon' }, svg),
}));

import LinuxDoIcon from './LinuxDoIcon';
import OIDCIcon from './OIDCIcon';
import WeChatIcon from './WeChatIcon';

const svgOf = (container) => container.querySelector('svg');

describe('logo icons', () => {
  it('LinuxDo renders a 16-unit square icon with artwork', () => {
    const { container } = render(<LinuxDoIcon />);

    const svg = svgOf(container);
    expect(svg).toHaveAttribute('viewBox', '0 0 16 16');
    expect(svg.querySelectorAll('path').length).toBeGreaterThan(0);
  });

  it('OIDC renders a 1024-unit icon at 20px', () => {
    const { container } = render(<OIDCIcon />);

    const svg = svgOf(container);
    expect(svg).toHaveAttribute('viewBox', '0 0 1024 1024');
    expect(svg).toHaveAttribute('width', '20');
    expect(svg).toHaveAttribute('height', '20');
  });

  it('WeChat wraps its icon in a block container', () => {
    const { container } = render(<WeChatIcon />);

    expect(container.firstChild.tagName).toBe('DIV');
    expect(svgOf(container)).toHaveAttribute('viewBox', '0 0 1024 1024');
  });
});
