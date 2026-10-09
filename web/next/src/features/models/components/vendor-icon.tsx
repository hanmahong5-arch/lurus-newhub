import { cn } from '@/lib/utils';

// Hue from the vendor name so one vendor always gets the same tile.
function hue(name: string): number {
  let h = 0;
  for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) % 360;
  return h;
}

/**
 * Vendor mark. The brand-logo package is not a dependency of this app yet
 * (see the lane report), so this draws the vendor's initial on a stable
 * per-vendor tile; swap the body for the logo component once it is added.
 */
export function VendorIcon(props: {
  vendor: string;
  size?: number;
  className?: string;
}) {
  const size = props.size ?? 28;
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
