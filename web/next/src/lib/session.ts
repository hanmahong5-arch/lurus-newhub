import { http } from '@/lib/api'

/** Same exit the legacy console uses: end the session, then back to /login. */
export async function signOut(): Promise<void> {
  try {
    await http.post('/api/v2/auth/zita-logout', undefined, {
      skipAuthRedirect: true,
    })
  } catch {
    // The session may already be gone; leaving is the goal either way.
  }
  try {
    window.localStorage.removeItem('user')
    window.localStorage.removeItem('tenant_slug')
  } catch {
    // Storage can be denied.
  }
  window.location.assign('/login')
}
