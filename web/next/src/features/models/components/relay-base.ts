/** Origin the relay answers on, for the copy-paste quick start. */
export function relayBase(): string {
  return typeof window === 'undefined' ? '' : window.location.origin;
}
