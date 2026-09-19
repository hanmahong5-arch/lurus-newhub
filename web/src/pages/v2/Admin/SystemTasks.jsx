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
import React, { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import { API } from '../../../helpers';

/*
 * v2 admin — background-task heartbeats (L3, 2026-09-13). Wired to
 * GET /api/v2/admin/system/tasks, which lists every registered periodic
 * background job (taskreg) on THIS pod, cross-referenced against each
 * job's metrics.LeaderTaskLastSuccess gauge.
 *
 * This is a per-pod health view, not a cluster-wide aggregator — the same
 * caveat the Gateway health page states for circuit-breaker state. A
 * "standby" row is expected, not a fault: either a follower replica for a
 * leader-gated task (that task has never been this replica's job to run),
 * or a task the operator has administratively disabled
 * (task.standby_reason distinguishes the two — see
 * internal/adapter/handler/v2_admin_system_tasks.go).
 *
 * Cycle 12 L3, repair round. Two things were wrong with the failure paths.
 *
 * (1) Every failure that was not an HTTP 403 fell through to `data === null`,
 *     which renders `tasks = []` — and an empty task list on this page reads
 *     as "no background job is late". A 502 from a rolling update therefore
 *     said the schedule was clean.
 * (2) A 200 carrying success:false was treated as a permission refusal. That
 *     was true of the old v1-shaped authHelper fallback; cycle 12 L4 changed
 *     RootJWTAuth's session fallback to answer 403 PERMISSION_DENIED and 401
 *     UNAUTHENTICATED (internal/adapter/middleware/admin_jwt_auth.go), so a
 *     success:false here now means the handler failed, not that the caller is
 *     unwelcome — showing "Root access required" for a broken registry sends
 *     the operator to ask for a permission they already have.
 *
 * Four outcomes now: tasks / 403 forbidden / 401 signed-out / error.
 */

const POLL_MS = 15000;
// While in the error state, keep checking — but slowly. Stopping outright
// means one transient 502 during a rolling update freezes the page until a
// human notices the retry button; polling at 15 s hammers a dead endpoint.
const ERROR_POLL_MS = 30000;

const stateClass = (s) =>
  s === 'overdue' ? 'tag error' : s === 'standby' ? 'tag' : 'tag ok';

const fmtInterval = (secs) => {
  const n = Number(secs ?? 0);
  if (n <= 0) return '—';
  if (n % 3600 === 0) return `${n / 3600}h`;
  if (n % 60 === 0) return `${n / 60}m`;
  return `${n}s`;
};

const fmtWhen = (unix) => {
  if (!unix) return null;
  return new Date(unix * 1000).toLocaleString();
};

const V2AdminSystemTasks = () => {
  const { t: tr } = useTranslation();
  const [data, setData] = useState(null);
  const [loading, setLoading] = useState(true);
  const [forbidden, setForbidden] = useState(false);
  const [signedOut, setSignedOut] = useState(false);
  // null when the last fetch succeeded; otherwise the backend message, or ''
  // when there was none.
  const [error, setError] = useState(null);
  const [live, setLive] = useState(true);

  const fetchTasks = useCallback(async () => {
    try {
      const res = await API.get('/api/v2/admin/system/tasks');
      if (res?.data?.success) {
        setData(res.data.data);
        setError(null);
      } else {
        // Post-L4 this is a handler failure, not a refusal: the refusal
        // shapes are 403 and 401, both of which arrive as HTTP errors.
        setError(res?.data?.message ?? '');
      }
    } catch (err) {
      const status = err?.response?.status;
      if (status === 403) {
        setForbidden(true);
        setError(null);
      } else if (status === 401) {
        setSignedOut(true);
        setError(null);
      } else {
        setError(err?.response?.data?.message ?? '');
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchTasks();
  }, [fetchTasks]);

  useEffect(() => {
    if (!live || forbidden || signedOut) return undefined;
    const id = setInterval(
      fetchTasks,
      error !== null ? ERROR_POLL_MS : POLL_MS,
    );
    return () => clearInterval(id);
  }, [live, forbidden, signedOut, error, fetchTasks]);

  const retry = useCallback(() => {
    setError(null);
    setLoading(true);
    fetchTasks();
  }, [fetchTasks]);

  const tasks = data?.tasks ?? [];
  const overdueCount = tasks.filter((t) => t.state === 'overdue').length;

  return (
    <HFShell
      active='admin-system-tasks'
      crumbs={[
        tr('console.admin.system_tasks.crumb_admin', 'operations & insights'),
        tr('console.admin.system_tasks.crumb', 'background tasks'),
      ]}
      actions={
        !forbidden &&
        !signedOut && (
          <button
            type='button'
            className='btn ghost'
            data-testid='system-tasks-live-toggle'
            onClick={() => setLive((v) => !v)}
          >
            {live
              ? tr('console.admin.system_tasks.pause', '❚❚ pause')
              : tr('console.admin.system_tasks.resume', '▶ live')}
          </button>
        )
      }
    >
      <div className='hf-page-head'>
        <div>
          <div className='lbl' style={{ marginBottom: 6 }}>
            {tr('console.admin.system_tasks.heading_lbl', 'background tasks')}
          </div>
          <h1 data-testid='system-tasks-headline'>
            {loading
              ? '…'
              : forbidden
                ? tr(
                    'console.admin.system_tasks.forbidden_title',
                    'Root access required',
                  )
                : signedOut
                  ? tr(
                      'console.admin.session_expired_title',
                      'Your session has expired',
                    )
                  : error !== null
                    ? tr(
                        'console.admin.system_tasks.error_title',
                        'Task schedule unknown',
                      )
                    : overdueCount > 0
                      ? tr(
                          'console.admin.system_tasks.overdue_count',
                          '{{count}} tasks overdue',
                          { count: overdueCount },
                        )
                      : tr(
                          'console.admin.system_tasks.all_ok',
                          'all tasks on schedule',
                        )}
          </h1>
          <div className='sub'>
            {tr(
              'console.admin.system_tasks.sub',
              'periodic background jobs · this pod · no cluster-wide aggregation',
            )}
          </div>
        </div>
      </div>

      {forbidden ? (
        <div style={{ padding: 24 }}>
          <div className='panel' style={{ padding: '20px 24px' }}>
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.system_tasks.forbidden_title',
                'Root access required',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12 }}>
              {tr(
                'console.admin.system_tasks.forbidden_body',
                'You do not have permission to view background tasks. Contact a platform administrator.',
              )}
            </div>
          </div>
        </div>
      ) : signedOut ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='system-tasks-signed-out'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.session_expired_title',
                'Your session has expired',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12, marginBottom: 12 }}>
              {tr(
                'console.admin.session_expired_body',
                'The server no longer recognises this session, so nothing on this page can be read. Sign in again to continue.',
              )}
            </div>
            <a
              className='btn sm'
              href='/login'
              data-testid='system-tasks-sign-in'
            >
              {tr('console.admin.sign_in_again', 'sign in again')}
            </a>
          </div>
        </div>
      ) : error !== null ? (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '20px 24px' }}
            data-testid='system-tasks-error'
          >
            <div className='strong' style={{ marginBottom: 6 }}>
              {tr(
                'console.admin.system_tasks.error_title',
                'Task schedule unknown',
              )}
            </div>
            <div className='muted' style={{ fontSize: 12, marginBottom: 12 }}>
              {tr(
                'console.admin.system_tasks.error_body',
                'The background-task endpoint did not answer. This is not a report that every job is on schedule — nothing about the schedule is known until the call succeeds. Retrying every 30s.',
              )}
            </div>
            {error ? (
              <div
                className='mono muted'
                style={{ fontSize: 11, marginBottom: 12 }}
              >
                {error}
              </div>
            ) : null}
            <button
              type='button'
              className='btn sm'
              data-testid='system-tasks-retry'
              onClick={retry}
            >
              {tr('console.admin.system_tasks.retry', 'retry')}
            </button>
          </div>
        </div>
      ) : (
        <div style={{ padding: 24 }}>
          <div
            className='panel'
            style={{ padding: '12px 16px', marginBottom: 16 }}
            data-testid='system-tasks-caveat'
          >
            <div className='muted' style={{ fontSize: 11, lineHeight: 1.7 }}>
              {tr(
                'console.admin.system_tasks.caveat',
                'This view is per-pod: it lists only the tasks this replica has registered and stamped. A "standby" task is either leader-gated (this replica does not currently hold the HA lease) or administratively disabled — that is expected, not a fault.',
              )}
            </div>
          </div>

          <div className='panel'>
            {loading ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
              >
                {tr('console.common.loading', 'Loading…')}
              </div>
            ) : tasks.length === 0 ? (
              <div
                className='muted'
                style={{ padding: '20px 24px', fontSize: 12 }}
                data-testid='system-tasks-empty'
              >
                {tr(
                  'console.admin.system_tasks.empty',
                  'No background task has registered on this pod yet.',
                )}
              </div>
            ) : (
              <table className='t'>
                <thead>
                  <tr>
                    <th>{tr('console.admin.system_tasks.th_name', 'task')}</th>
                    <th>
                      {tr('console.admin.system_tasks.th_interval', 'interval')}
                    </th>
                    <th>
                      {tr(
                        'console.admin.system_tasks.th_leader_only',
                        'leader-only',
                      )}
                    </th>
                    <th>
                      {tr(
                        'console.admin.system_tasks.th_last_success',
                        'last success',
                      )}
                    </th>
                    <th>
                      {tr('console.admin.system_tasks.th_state', 'state')}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((task) => (
                    <tr
                      key={task.name}
                      data-testid={`system-tasks-row-${task.name}`}
                    >
                      <td className='mono'>{task.name}</td>
                      <td className='mono muted'>
                        {fmtInterval(task.interval_seconds)}
                      </td>
                      <td className='mono muted'>
                        {task.leader_only
                          ? tr('console.admin.system_tasks.yes', 'yes')
                          : tr('console.admin.system_tasks.no', 'no')}
                      </td>
                      <td className='mono muted'>
                        {fmtWhen(task.last_success_at) ??
                          tr('console.admin.system_tasks.never', 'never')}
                      </td>
                      <td>
                        <span
                          className={stateClass(task.state)}
                          data-testid={`system-tasks-state-${task.name}`}
                        >
                          {tr(
                            `console.admin.system_tasks.state_${task.state}`,
                            task.state,
                          )}
                        </span>
                        {task.state === 'standby' && task.standby_reason && (
                          <span
                            className='muted'
                            style={{ fontSize: 11, marginLeft: 6 }}
                            data-testid={`system-tasks-reason-${task.name}`}
                          >
                            (
                            {tr(
                              `console.admin.system_tasks.standby_reason_${task.standby_reason}`,
                              task.standby_reason,
                            )}
                            )
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {data && (
            <div className='muted' style={{ fontSize: 11, marginTop: 10 }}>
              {tr('console.admin.system_tasks.pod_label', 'pod')}: {data.pod} ·{' '}
              {tr('console.admin.system_tasks.is_leader_label', 'leader')}:{' '}
              {data.is_leader
                ? tr('console.admin.system_tasks.yes', 'yes')
                : tr('console.admin.system_tasks.no', 'no')}
            </div>
          )}
        </div>
      )}
    </HFShell>
  );
};

export default V2AdminSystemTasks;
