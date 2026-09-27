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
import { useCallback, useEffect, useRef, useState } from 'react';
import { API } from '../../helpers';
import { classifyLoad } from '../../helpers/loadState';

/*
 * The one read path for a v2 console page (cycle-18 L1).
 *
 * Before this hook every page fetched on mount with its own try/catch, and
 * seven of them (failed_read_is_not_empty.test.jsx's KNOWN_UNFIXED) ended
 * every non-2xx outcome in `catch (_) {}` — so a 500 from the upstream, a
 * dropped connection or a 200 {success:false} rendered the exact page a
 * genuinely empty account sees: "0 upstream channels · healthy 0", "No
 * tokens yet. Create one to get started.", a price table saying "no data".
 * An operator reads those as findings. They were never measured.
 *
 * The hook does not decide what a failure looks like — that is HfLoadError
 * — it only refuses to let one look like data:
 *   - `data` is null unless the read answered 2xx with success:true, so
 *     nothing downstream can count, sum or list a body that never came;
 *   - `status` is classifyLoad()'s vocabulary ('ok' | 'error' |
 *     'unauthenticated' | 'forbidden', null before the first answer), so
 *     the page can say "sign in again" or "ask for access" instead of
 *     "try again" without re-deriving it from the HTTP status;
 *   - `error` is the raw outcome (the rejected axios error, or the response
 *     whose body reported failure) for pages that want the server's message;
 *   - `retry()` re-runs the read and resolves when it has settled, which is
 *     what HfLoadError awaits to show its "Retrying…" state, and what a
 *     post-mutation refresh awaits before it reads the new list.
 *
 * skipErrorHandler is always on: the page now owns the failure surface, and
 * the interceptor's toast on top of a retry panel would be the same failure
 * said twice (once transiently, once honestly).
 *
 * `path` falsy or `enabled` false reads nothing and reports status null —
 * "have not asked", which is different from "asked and got nothing". `deps`
 * re-runs the read for inputs the path does not encode (a refresh tick).
 * `parse(body.data, body)` shapes the successful body; it is not called on
 * failure, so it never has to defend against a null.
 */
export const useTenantRead = (
  path,
  { deps = [], enabled = true, parse } = {},
) => {
  const active = Boolean(enabled && path);
  const [state, setState] = useState({
    data: null,
    error: null,
    status: null,
    loading: active,
  });
  // Monotonic request id: a response that is not the newest request's is
  // dropped, so a slug switch, a filter change or an unmount can never let
  // a slow earlier answer overwrite a newer one.
  const seq = useRef(0);
  const parseRef = useRef(parse);
  parseRef.current = parse;

  const run = useCallback(async () => {
    const id = ++seq.current;
    setState((s) => ({ ...s, loading: true }));
    let outcome;
    try {
      outcome = await API.get(path, { skipErrorHandler: true });
    } catch (err) {
      outcome = err;
    }
    if (id !== seq.current) return;
    const status = classifyLoad(outcome);
    if (status === 'ok') {
      const body = outcome.data;
      const p = parseRef.current;
      setState({
        data: p ? p(body.data, body) : body.data,
        error: null,
        status,
        loading: false,
      });
    } else {
      setState({ data: null, error: outcome, status, loading: false });
    }
    // `deps` is the caller's own re-read trigger, spread on purpose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, ...deps]);

  useEffect(() => {
    if (!active) return undefined;
    run();
    return () => {
      seq.current += 1;
    };
  }, [active, run]);

  const retry = useCallback(
    () => (active ? run() : Promise.resolve()),
    [active, run],
  );

  return { ...state, retry };
};

export default useTenantRead;
