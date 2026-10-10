/**
 * Session handling for the new console.
 *
 * The new console has no sign-in screen. The session is the same gin session
 * cookie the legacy console uses; when it is missing or expired the browser is
 * sent to the legacy /login page with `?redirect=<current /next/... path>` and
 * that page brings the person back (web/src/helpers/loginRedirect.js).
 */

/** Role levels as stored on the user row (web/src/helpers/auth.jsx). */
export const ROLE = {
  guest: 0,
  user: 1,
  admin: 10,
  root: 100,
} as const

export function hasMinRole(role: number | undefined, minRole = 0): boolean {
  return typeof role === 'number' && role >= minRole
}

/** Legacy sign-in URL carrying the page to come back to. */
export function buildLoginUrl(returnPath: string): string {
  return `/login?redirect=${encodeURIComponent(returnPath)}`
}

let redirecting = false

/**
 * Leave for the sign-in page. `returnPath` defaults to the current location
 * (full path, including the /next basename). Safe to call from a burst of
 * parallel 401s: only the first call navigates.
 */
export function redirectToLogin(returnPath?: string): void {
  if (typeof window === 'undefined' || redirecting) return
  const { pathname, search, hash } = window.location
  // Already on a sign-in route: bouncing would loop.
  if (/^\/(login|bridge-login|register)(\/|$)/.test(pathname)) return
  redirecting = true
  try {
    // The legacy console keeps a durable `user` hint; if it survived an
    // expired session, the login route would bounce straight back to
    // /console instead of honouring ?redirect=.
    window.localStorage.removeItem('user')
  } catch {
    // Storage can be denied (private mode); the redirect still works.
  }
  window.location.assign(buildLoginUrl(returnPath ?? pathname + search + hash))
}

/** Test seam: forget that a redirect already happened. */
export function resetRedirectGuard(): void {
  redirecting = false
}
