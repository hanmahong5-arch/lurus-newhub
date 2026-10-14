import type { NavItem } from './nav'

function normalize(path: string): string {
  if (path.length > 1 && path.endsWith('/')) return path.slice(0, -1)
  return path
}

/** True when `pathname` is `to` itself or lives below it. */
export function pathMatches(pathname: string, to: string): boolean {
  const current = normalize(pathname)
  const target = normalize(to)
  return current === target || current.startsWith(`${target}/`)
}

/**
 * The one nav entry that owns `pathname`: the longest matching `to`. A bare
 * prefix test would also light `/usage-logs` up on `/usage-logs/audit`.
 */
export function activeNavItemId(
  pathname: string,
  items: readonly Pick<NavItem, 'id' | 'to'>[]
): string | null {
  let best: Pick<NavItem, 'id' | 'to'> | null = null
  for (const item of items) {
    if (!pathMatches(pathname, item.to)) continue
    if (best === null || item.to.length > best.to.length) best = item
  }
  return best?.id ?? null
}
