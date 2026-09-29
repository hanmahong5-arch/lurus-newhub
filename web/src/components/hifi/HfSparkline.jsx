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

// HfSparkline — a bare inline trend line for a KPI card, after the
// Vercel/Stripe/OpenRouter pattern of a tiny chart living next to the
// number it explains. No axes, no grid, no interactivity: it is a shape,
// not a chart. Single-colour (currentColor), so it inherits whatever ink
// the caller's KPI card is using.
//
// Fewer than 2 points, or every point 0, has no shape to draw — rendering a
// flat/degenerate line there would assert a trend that isn't there, so it
// renders a same-sized empty placeholder instead (layout never jumps when
// the underlying data arrives).

/**
 * @param {object} p
 * @param {number[]} p.points   values, oldest first
 * @param {number} [p.width=64]
 * @param {number} [p.height=18]
 * @param {boolean} [p.title]   when true, adds an SVG <title> tooltip whose
 *   content is the last point's value
 */
const HfSparkline = ({ points, width = 64, height = 18, title = false }) => {
  const values = Array.isArray(points) ? points.map((v) => Number(v) || 0) : [];
  const hasSignal = values.length >= 2 && values.some((v) => v !== 0);

  if (!hasSignal) {
    return (
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        aria-hidden='true'
        data-testid='hf-sparkline-empty'
      />
    );
  }

  const max = Math.max(...values, 0);
  const min = Math.min(...values, 0);
  const range = max - min || 1;
  const step = width / (values.length - 1);
  // A hair of vertical padding so a peak/trough doesn't clip the stroke.
  const pad = 2;
  const plotHeight = Math.max(height - pad * 2, 1);
  const coords = values
    .map((v, i) => {
      const x = i * step;
      const y = pad + plotHeight - ((v - min) / range) * plotHeight;
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  const last = values[values.length - 1];

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      aria-hidden='true'
    >
      {title && <title>{last}</title>}
      <polyline
        points={coords}
        fill='none'
        stroke='currentColor'
        strokeWidth='1.5'
        strokeLinecap='round'
        strokeLinejoin='round'
      />
    </svg>
  );
};

export default HfSparkline;
