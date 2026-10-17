/**
 * The platform's top-up link with `return_to` set to the given page, so the
 * platform sends the user back after paying (cross-product contract:
 * `/topup?amount=&return_to=`, host given by the platform, never hard-coded).
 * A return_to the platform already set is kept; a link that does not parse
 * is passed through unchanged.
 */
export function withReturnTo(topupUrl: string, returnTo: string): string {
  try {
    const url = new URL(topupUrl)
    if (!url.searchParams.has('return_to')) {
      url.searchParams.set('return_to', returnTo)
    }
    return url.toString()
  } catch {
    return topupUrl
  }
}
