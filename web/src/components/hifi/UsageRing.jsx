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
import { useTranslation } from 'react-i18next';

/**
 * UsageRing — SVG donut ring showing quota consumption.
 *
 * Color thresholds:
 *   < 70%  → --hf-ok   (green)
 *   70-90% → --hf-warn (amber)
 *   > 90%  → --hf-err  (red)
 *
 * Props:
 *   used    {number} — consumed amount
 *   total   {number} — total quota (0 → show "unlimited" ring at 0%)
 *   label   {string} — metric name displayed below the ring
 *   unit    {string} — e.g. "tokens", "$", "req"
 *   size    {number} — SVG dimension in px (default 64)
 *   compact {boolean} — ring only: no caption, no used/total row (for a card
 *                       whose own title and figure already say what it is)
 */
const UsageRing = ({
  used = 0,
  total = 0,
  label = '',
  unit = '',
  size = 64,
  compact = false,
}) => {
  const { t: tr } = useTranslation();
  const pct = total > 0 ? Math.min(used / total, 1) : 0;

  const stroke = 5;
  const cx = size / 2;
  const cy = size / 2;
  const r = cx - stroke;
  const circumference = 2 * Math.PI * r;
  const dash = pct * circumference;
  const gap = circumference - dash;

  let color;
  if (pct >= 0.9) {
    color = 'var(--hf-err)';
  } else if (pct >= 0.7) {
    color = 'var(--hf-warn)';
  } else {
    color = 'var(--hf-ok)';
  }

  const fmt = (n) => {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(0)}k`;
    return String(n);
  };

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 4,
        fontFamily: 'var(--hf-mono)',
      }}
    >
      <svg
        width={size}
        height={size}
        viewBox={`0 0 ${size} ${size}`}
        role='img'
        aria-label={tr(
          'console.component.usage_ring.aria_label',
          '{{label}}: {{pct}}% used',
          { label, pct: Math.round(pct * 100) },
        )}
      >
        {/* Track */}
        <circle
          cx={cx}
          cy={cy}
          r={r}
          fill='none'
          stroke='var(--hf-rule)'
          strokeWidth={stroke}
        />
        {/* Progress arc — starts from top (−90°) */}
        <circle
          cx={cx}
          cy={cy}
          r={r}
          fill='none'
          stroke={color}
          strokeWidth={stroke}
          strokeDasharray={`${dash} ${gap}`}
          strokeLinecap='butt'
          transform={`rotate(-90 ${cx} ${cy})`}
          style={{ transition: 'stroke-dasharray 0.4s ease' }}
        />
        {/* Centre percentage */}
        <text
          x={cx}
          y={cy}
          textAnchor='middle'
          dominantBaseline='central'
          fontSize={size < 60 ? 9 : 11}
          fontFamily='var(--hf-mono)'
          fill='var(--hf-ink)'
          fontWeight='500'
        >
          {total > 0 ? `${Math.round(pct * 100)}%` : '∞'}
        </text>
      </svg>

      {!compact && (
        <>
          {/* Label row */}
          <div
            style={{
              fontSize: 9,
              textTransform: 'uppercase',
              letterSpacing: '0.12em',
              color: 'var(--hf-ink-3)',
              textAlign: 'center',
            }}
          >
            {label}
          </div>

          {/* Used / total */}
          {total > 0 && (
            <div
              style={{
                fontSize: 10,
                color: 'var(--hf-ink-2)',
                textAlign: 'center',
              }}
            >
              {fmt(used)}
              <span style={{ color: 'var(--hf-ink-4)' }}>/{fmt(total)}</span>
              {unit && (
                <span style={{ marginLeft: 2, color: 'var(--hf-ink-4)' }}>
                  {unit}
                </span>
              )}
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default UsageRing;
