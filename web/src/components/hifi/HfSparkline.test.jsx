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
import { render } from '@testing-library/react';
import HfSparkline from './HfSparkline';

describe('HfSparkline', () => {
  it('renders a polyline with one point per input value', () => {
    const { container } = render(
      <HfSparkline points={[1, 4, 2, 8, 5, 9, 3]} />,
    );
    const polyline = container.querySelector('polyline');
    expect(polyline).toBeTruthy();
    const pts = polyline.getAttribute('points').trim().split(/\s+/);
    expect(pts).toHaveLength(7);
  });

  it('renders a same-sized placeholder — no polyline — when every value is 0', () => {
    const { container } = render(<HfSparkline points={[0, 0, 0, 0]} />);
    expect(container.querySelector('polyline')).toBeNull();
    const svg = container.querySelector('svg');
    expect(svg).toBeTruthy();
    expect(svg.getAttribute('width')).toBe('64');
    expect(svg.getAttribute('height')).toBe('18');
  });

  it('renders the same placeholder for fewer than 2 points', () => {
    const { container } = render(<HfSparkline points={[42]} />);
    expect(container.querySelector('polyline')).toBeNull();
  });

  it('renders the same placeholder for empty/missing points, without throwing', () => {
    const { container: c1 } = render(<HfSparkline points={[]} />);
    expect(c1.querySelector('polyline')).toBeNull();
    const { container: c2 } = render(<HfSparkline />);
    expect(c2.querySelector('polyline')).toBeNull();
  });

  it('is aria-hidden — the text sibling carries the meaning', () => {
    const { container } = render(<HfSparkline points={[1, 2, 3]} />);
    expect(container.querySelector('svg').getAttribute('aria-hidden')).toBe(
      'true',
    );
  });

  it('adds a <title> with the last point value only when title=true', () => {
    const { container: withTitle } = render(
      <HfSparkline points={[1, 2, 3]} title />,
    );
    expect(withTitle.querySelector('title').textContent).toBe('3');

    const { container: withoutTitle } = render(
      <HfSparkline points={[1, 2, 3]} />,
    );
    expect(withoutTitle.querySelector('title')).toBeNull();
  });

  it('honors custom width/height', () => {
    const { container } = render(
      <HfSparkline points={[1, 2, 3]} width={100} height={30} />,
    );
    const svg = container.querySelector('svg');
    expect(svg.getAttribute('width')).toBe('100');
    expect(svg.getAttribute('height')).toBe('30');
  });
});
