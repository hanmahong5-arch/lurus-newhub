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
import { useCallback, useEffect, useRef, useState } from 'react'

/**
 * Form-draft persistence: a half-typed top-up or ceiling survives an
 * accidental tab close. Drafts are namespaced per person (switching accounts
 * on one browser never crosses them), versioned, and expire after a week.
 */
export const DRAFT_NAMESPACE_PREFIX = 'lurus-hub:draft:'
export const DRAFT_DEFAULT_DEBOUNCE_MS = 300
export const DRAFT_DEFAULT_TTL_MS = 7 * 24 * 60 * 60 * 1000
export const DRAFT_SCHEMA_VERSION = 1

interface Envelope<T> {
  v: number
  d: T
  t: number
}

export function draftKey(userId: string | number, storageKey: string): string {
  return `${DRAFT_NAMESPACE_PREFIX}${userId}:${storageKey}`
}

function readDraft<T>(
  fullKey: string,
  version: number,
  ttlMs: number
): T | undefined {
  try {
    const raw = window.localStorage.getItem(fullKey)
    if (!raw) return undefined
    const envelope = JSON.parse(raw) as Partial<Envelope<T>> | null
    if (
      !envelope ||
      typeof envelope !== 'object' ||
      envelope.v !== version ||
      typeof envelope.t !== 'number' ||
      Date.now() - envelope.t > ttlMs
    ) {
      window.localStorage.removeItem(fullKey)
      return undefined
    }
    return envelope.d
  } catch {
    return undefined
  }
}

function writeDraft<T>(fullKey: string, version: number, value: T): void {
  try {
    const envelope: Envelope<T> = { v: version, d: value, t: Date.now() }
    window.localStorage.setItem(fullKey, JSON.stringify(envelope))
  } catch {
    // Storage can be unavailable (private mode, quota): the form still works.
  }
}

interface DraftOptions {
  userId?: string | number
  debounceMs?: number
  ttlMs?: number
}

export function useFormDraft<T>(
  storageKey: string,
  initialValue: T,
  options: DraftOptions = {}
) {
  const {
    userId = 'anon',
    debounceMs = DRAFT_DEFAULT_DEBOUNCE_MS,
    ttlMs = DRAFT_DEFAULT_TTL_MS,
  } = options
  const fullKey = draftKey(userId, storageKey)
  const keyRef = useRef(fullKey)
  const initialRef = useRef(initialValue)

  const [state, setState] = useState(() => {
    const stored = readDraft<T>(fullKey, DRAFT_SCHEMA_VERSION, ttlMs)
    return {
      draft: stored === undefined ? initialValue : stored,
      restored: stored !== undefined,
    }
  })

  const latest = useRef<T>(state.draft)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const pending = useRef(false)

  const setDraft = useCallback(
    (update: T | ((previous: T) => T)) => {
      const next =
        typeof update === 'function'
          ? (update as (previous: T) => T)(latest.current)
          : update
      latest.current = next
      setState((s) => ({ ...s, draft: next }))
      if (timer.current) clearTimeout(timer.current)
      pending.current = true
      timer.current = setTimeout(() => {
        writeDraft(keyRef.current, DRAFT_SCHEMA_VERSION, latest.current)
        pending.current = false
        timer.current = null
      }, debounceMs)
    },
    [debounceMs]
  )

  const clear = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = null
    pending.current = false
    try {
      window.localStorage.removeItem(keyRef.current)
    } catch {
      // Nothing stored that could be removed.
    }
    latest.current = initialRef.current
    setState({ draft: initialRef.current, restored: false })
  }, [])

  // A quick edit followed by navigating away must not lose the last keystroke.
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
      if (pending.current) {
        writeDraft(keyRef.current, DRAFT_SCHEMA_VERSION, latest.current)
      }
    },
    []
  )

  return {
    draft: state.draft,
    setDraft,
    clear,
    /** The draft came from a previous visit rather than the defaults. */
    restored: state.restored,
  }
}
