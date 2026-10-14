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
import { getLobeIcon } from '@/components/lobe-icon';
import { cn } from '@/lib/utils';

// Hue from the vendor name so one vendor always gets the same tile.
function hue(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) % 360;
  return h;
}

/**
 * Brand mark for a model. `icon` is the brand icon key the server sends with
 * the model; it is never derived from the model name. Without one the vendor's
 * initial is drawn on a stable per-vendor tile.
 */
export function VendorIcon(props: {
  vendor: string;
  icon?: string;
  size?: number;
  className?: string;
}) {
  const size = props.size ?? 28;
  if (props.icon) {
    return (
      <span
        aria-hidden='true'
        data-testid='vendor-icon'
        className={cn(
          'inline-flex shrink-0 items-center justify-center',
          props.className,
        )}
        style={{ width: size, height: size }}
      >
        {getLobeIcon(props.icon, size)}
      </span>
    );
  }
  const label = props.vendor.trim();
  const h = hue(label.toLowerCase());
  return (
    <span
      aria-hidden='true'
      data-testid='vendor-icon'
      className={cn(
        'inline-flex shrink-0 items-center justify-center rounded-md font-semibold uppercase select-none',
        props.className,
      )}
      style={{
        width: size,
        height: size,
        fontSize: Math.round(size * 0.46),
        background: label ? `hsl(${h} 60% 92%)` : 'var(--muted)',
        color: label ? `hsl(${h} 55% 32%)` : 'var(--muted-foreground)',
      }}
    >
      {label ? label.charAt(0) : '?'}
    </span>
  );
}
