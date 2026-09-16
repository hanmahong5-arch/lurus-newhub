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
import React, { useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import { API, showError } from '../../../helpers';
import { formatUSD } from '../../../helpers/formatting';

/*
 * v2 Tasks page — the destination behind the nav item that today ships
 * disabled:true ("Midjourney / async task logs not available in v2 yet",
 * web/src/components/hifi/HFShell.jsx). Wired to the two self-scoped async
 * job list endpoints, both already live:
 *   GET /api/task/self/  (handler.GetUserTask,  internal/adapter/handler/task.go)
 *   GET /api/mj/self/    (handler.GetUserMidjourney, internal/adapter/handler/midjourney.go)
 * Neither takes a tenant_slug — both scope by the session's user id
 * server-side (c.GetInt("id")), so this page does not read useTenantSlug.
 *
 * This file is new and self-contained; it does not register a route or a
 * nav entry — HFShell.jsx and App.jsx are out of scope for this lane.
 *
 * Contract for the caller that mounts this page:
 *   import HFTasks from '.../pages/v2/Tasks';   // default export
 *   <HFTasks />                                  // no required props
 */

const PAGE_SIZE = 50;

// ─── shared cell helpers ────────────────────────────────────────────────────

// Task.SubmitTime/FinishTime are stored in whole Unix seconds
// (repo.InitTask -> SubmitTime: time.Now().Unix(), internal/adapter/repo/task.go:100).
// Midjourney.SubmitTime/FinishTime are stored in Unix *milliseconds*
// (internal/app/relay/mjproxy_handler.go:566 and :604 both write
// time.Now().UnixNano() / int64(time.Millisecond)). The two async job
// tables were never unified onto one clock, so this page keeps the two
// tabs' time math separate rather than guessing a shared unit.
const fmtTaskTime = (sec) =>
  sec ? new Date(sec * 1000).toLocaleString() : '—';
const fmtMjTime = (ms) => (ms ? new Date(ms).toLocaleString() : '—');

const taskDurationSec = (submitSec, finishSec) =>
  submitSec && finishSec ? finishSec - submitSec : null;
const mjDurationSec = (submitMs, finishMs) =>
  submitMs && finishMs ? (finishMs - submitMs) / 1000 : null;

// Status values as actually written by the two job runners — not invented.
// Both Task and Midjourney share the same TaskStatus string enum
// (internal/domain/entity/task.go:31-37: NOT_START/SUBMITTED/QUEUED/
// IN_PROGRESS/FAILURE/SUCCESS/UNKNOWN); the empty string is the pre-SUBMITTED
// state a freshly-created row starts in (legacy TaskLogsColumnDefs.jsx
// renderStatus case ''). Midjourney additionally uses MODAL for a job
// waiting on a follow-up user selection (legacy MjLogsColumnDefs.jsx).
const STATUS_META = {
  SUCCESS: ['tag ok', 'status_success', 'success'],
  FAILURE: ['tag err', 'status_failure', 'failed'],
  IN_PROGRESS: ['tag warn', 'status_in_progress', 'in progress'],
  SUBMITTED: ['tag warn', 'status_submitted', 'queued'],
  QUEUED: ['tag warn', 'status_queued', 'queued'],
  MODAL: ['tag warn', 'status_modal', 'awaiting selection'],
  NOT_START: ['tag', 'status_not_start', 'not started'],
  UNKNOWN: ['tag', 'status_unknown', 'unknown'],
};

const StatusTag = ({ status }) => {
  const { t: tr } = useTranslation();
  if (!status) {
    return (
      <span className='tag'>
        {tr('console.tasks.status_pending', 'submitting…')}
      </span>
    );
  }
  const meta = STATUS_META[status];
  if (!meta) return <span className='tag'>{status}</span>;
  const [cls, key, fallback] = meta;
  return <span className={cls}>{tr(`console.tasks.${key}`, fallback)}</span>;
};

// Progress is a free-text field ("45%", a provider-specific string, or
// empty) — legacy TaskLogsColumnDefs/MjLogsColumnDefs both fall back to the
// raw text when it does not parse as a percentage. Mirrored here without the
// Semi <Progress> component (hi-fi shell does not import Semi UI).
const ProgressCell = ({ value, failed }) => {
  const pct = value ? parseInt(value.replace('%', ''), 10) : NaN;
  if (Number.isNaN(pct)) {
    return <span className='mono muted'>{value || '—'}</span>;
  }
  return (
    <div
      style={{
        width: 90,
        height: 6,
        background: 'var(--hf-sunken)',
        border: '1px solid var(--hf-rule)',
        borderRadius: 3,
        overflow: 'hidden',
      }}
      title={`${pct}%`}
    >
      <div
        style={{
          width: `${Math.max(0, Math.min(100, pct))}%`,
          height: '100%',
          background: failed ? 'var(--hf-err)' : 'var(--hf-accent)',
        }}
      />
    </div>
  );
};

const inputStyle = {
  fontFamily: 'var(--hf-mono)',
  fontSize: 11,
  padding: '4px 8px',
  border: '1px solid var(--hf-rule)',
  background: 'var(--hf-sunken)',
  color: 'var(--hf-ink)',
  borderRadius: 2,
  outline: 'none',
};

const cellStyle = { padding: '7px 10px', fontSize: 12 };

// ─── Tasks tab ──────────────────────────────────────────────────────────────

const TasksTab = ({ tr }) => {
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  const [taskId, setTaskId] = useState('');
  const [projectId, setProjectId] = useState('');
  const [requestId, setRequestId] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // project_id/request_id (cycle-8 L10, GetUserTask handler.go:381) are the
  // only two filters this endpoint added beyond task_id/date-range — both
  // are exact-match server-side (repo.TaskGetAllUserTask, task.go:118-152)
  // and both are sent as plain query values, matching the legacy
  // /api/task/self hook (web/src/hooks/task-logs/useTaskLogsData.js).
  const fetch = useCallback(
    async (targetPage, f) => {
      setLoading(true);
      try {
        const params = new URLSearchParams({
          p: String(targetPage),
          page_size: String(PAGE_SIZE),
        });
        if (f.taskId) params.set('task_id', f.taskId);
        if (f.projectId) params.set('project_id', f.projectId);
        if (f.requestId) params.set('request_id', f.requestId);
        if (f.start)
          params.set(
            'start_timestamp',
            String(Math.floor(new Date(f.start).getTime() / 1000)),
          );
        if (f.end)
          params.set(
            'end_timestamp',
            String(Math.floor(new Date(f.end).getTime() / 1000)),
          );
        const res = await API.get(`/api/task/self/?${params.toString()}`);
        if (res?.data?.success) {
          const d = res.data.data;
          setItems(d.items ?? []);
          setTotal(d.total ?? 0);
          setPage(d.page ?? targetPage);
        } else {
          showError(
            res?.data?.message ||
              tr('console.tasks.load_failed', 'Failed to load tasks'),
          );
        }
      } catch (_) {
        // error toast handled by API interceptor
      } finally {
        setLoading(false);
      }
    },
    [tr],
  );

  React.useEffect(() => {
    fetch(1, { taskId: '', projectId: '', requestId: '', start: '', end: '' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const search = () => fetch(1, { taskId, projectId, requestId, start, end });
  const clear = () => {
    setTaskId('');
    setProjectId('');
    setRequestId('');
    setStart('');
    setEnd('');
    fetch(1, { taskId: '', projectId: '', requestId: '', start: '', end: '' });
  };
  const goPage = (next) =>
    fetch(next, { taskId, projectId, requestId, start, end });

  return (
    <>
      <div
        style={{
          padding: '10px 28px',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          flexWrap: 'wrap',
        }}
      >
        <input
          style={{ ...inputStyle, width: 140 }}
          data-testid='tasks-task-id-filter'
          placeholder={tr('console.tasks.ph_task_id', 'task id…')}
          value={taskId}
          onChange={(e) => setTaskId(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && search()}
        />
        <input
          style={{ ...inputStyle, width: 120 }}
          data-testid='tasks-project-id-filter'
          placeholder={tr('console.tasks.ph_project_id', 'project id…')}
          value={projectId}
          onChange={(e) => setProjectId(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && search()}
        />
        <input
          style={{ ...inputStyle, width: 160 }}
          data-testid='tasks-request-id-filter'
          placeholder={tr('console.tasks.ph_request_id', 'request id…')}
          value={requestId}
          onChange={(e) => setRequestId(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && search()}
        />
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.start_time', 'start time')}
          value={start}
          onChange={(e) => setStart(e.target.value)}
        />
        <span className='muted' style={{ fontSize: 11 }}>
          →
        </span>
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.end_time', 'end time')}
          value={end}
          onChange={(e) => setEnd(e.target.value)}
        />
        <button
          type='button'
          className='btn primary'
          data-testid='tasks-search-btn'
          onClick={search}
        >
          {tr('console.common.search', 'search')}
        </button>
        <button
          type='button'
          className='btn ghost'
          data-testid='tasks-clear-btn'
          onClick={clear}
        >
          {tr('console.tasks.clear', 'clear')}
        </button>
      </div>

      {loading && (
        <div
          className='muted mono'
          style={{ padding: '20px 28px', fontSize: 12 }}
        >
          {tr('console.common.loading', 'loading…')}
        </div>
      )}

      {!loading && items.length === 0 && (
        <div
          className='muted'
          style={{ padding: '30px 28px', fontSize: 12 }}
          data-testid='tasks-empty'
        >
          {tr('console.tasks.empty_tasks', 'No tasks found.')}
        </div>
      )}

      {!loading && items.length > 0 && (
        <div className='hf-table-scroll' style={{ padding: '0 28px' }}>
          <table className='t' data-testid='tasks-table'>
            <thead>
              <tr>
                <th>{tr('console.tasks.th_submitted', 'submitted')}</th>
                <th>{tr('console.tasks.th_duration', 'duration')}</th>
                <th>{tr('console.tasks.th_type', 'type')}</th>
                <th>{tr('console.tasks.th_task_id', 'task id')}</th>
                <th>{tr('console.tasks.th_request_id', 'request id')}</th>
                <th>{tr('console.tasks.th_status', 'status')}</th>
                <th>{tr('console.tasks.th_progress', 'progress')}</th>
                <th>{tr('console.tasks.th_cost', 'cost')}</th>
                <th>{tr('console.tasks.th_detail', 'detail')}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => {
                const dur = taskDurationSec(row.submit_time, row.finish_time);
                const isUrl =
                  typeof row.fail_reason === 'string' &&
                  /^https?:\/\//.test(row.fail_reason);
                return (
                  <tr key={row.id} data-testid={`tasks-row-${row.id}`}>
                    <td className='mono' style={cellStyle}>
                      {fmtTaskTime(row.submit_time)}
                    </td>
                    <td className='mono' style={cellStyle}>
                      {dur != null ? `${dur}s` : '—'}
                    </td>
                    <td className='mono' style={cellStyle}>
                      {row.action || '—'}
                    </td>
                    <td
                      className='mono'
                      style={{ ...cellStyle, maxWidth: 160 }}
                      title={row.task_id}
                    >
                      {row.task_id || '—'}
                    </td>
                    <td
                      className='mono'
                      style={{ ...cellStyle, maxWidth: 160 }}
                      title={row.request_id}
                    >
                      {row.request_id || '—'}
                    </td>
                    <td style={cellStyle}>
                      <StatusTag status={row.status} />
                    </td>
                    <td style={cellStyle}>
                      <ProgressCell
                        value={row.progress}
                        failed={row.status === 'FAILURE'}
                      />
                    </td>
                    <td className='mono' style={cellStyle}>
                      {formatUSD(row.quota)}
                    </td>
                    <td style={{ ...cellStyle, maxWidth: 220 }}>
                      {isUrl ? (
                        <a
                          href={row.fail_reason}
                          target='_blank'
                          rel='noreferrer'
                        >
                          {tr('console.tasks.view_result', 'view result')}
                        </a>
                      ) : (
                        <span className='muted' title={row.fail_reason || ''}>
                          {row.fail_reason || '—'}
                        </span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          padding: '10px 28px',
          fontSize: 12,
        }}
      >
        <button
          type='button'
          className='btn ghost sm'
          disabled={page <= 1 || loading}
          onClick={() => goPage(page - 1)}
          data-testid='tasks-prev-btn'
        >
          {tr('console.common.prev', '← prev')}
        </button>
        <span className='mono muted' data-testid='tasks-range-label'>
          {tr(
            'console.tasks.range_of_total',
            '{{start}}–{{end}} of {{total}}',
            {
              start: total === 0 ? 0 : (page - 1) * PAGE_SIZE + 1,
              end: Math.min(page * PAGE_SIZE, total),
              total,
            },
          )}
        </span>
        <button
          type='button'
          className='btn ghost sm'
          disabled={page >= totalPages || loading}
          onClick={() => goPage(page + 1)}
          data-testid='tasks-next-btn'
        >
          {tr('console.common.next', 'next →')}
        </button>
      </div>
    </>
  );
};

// ─── MJ tab ─────────────────────────────────────────────────────────────────

const MjTab = ({ tr }) => {
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  const [mjId, setMjId] = useState('');
  const [start, setStart] = useState('');
  const [end, setEnd] = useState('');

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  // No project_id/request_id here — entity.Midjourney carries neither column
  // (internal/domain/entity/midjourney.go:3-26); that cost-attribution pair
  // only exists on the Task table (cycle-8 L10). start_timestamp/
  // end_timestamp are sent in *milliseconds* to match Midjourney.SubmitTime's
  // storage unit (see fmtMjTime above) — the legacy /api/mj/self hook
  // (web/src/hooks/mj-logs/useMjLogsData.js) sends raw Date.parse() output
  // for the same reason, unlike the Task tab which divides by 1000.
  const fetch = useCallback(
    async (targetPage, f) => {
      setLoading(true);
      try {
        const params = new URLSearchParams({
          p: String(targetPage),
          page_size: String(PAGE_SIZE),
        });
        if (f.mjId) params.set('mj_id', f.mjId);
        if (f.start)
          params.set('start_timestamp', String(new Date(f.start).getTime()));
        if (f.end)
          params.set('end_timestamp', String(new Date(f.end).getTime()));
        const res = await API.get(`/api/mj/self/?${params.toString()}`);
        if (res?.data?.success) {
          const d = res.data.data;
          setItems(d.items ?? []);
          setTotal(d.total ?? 0);
          setPage(d.page ?? targetPage);
        } else {
          showError(
            res?.data?.message ||
              tr('console.tasks.load_failed', 'Failed to load tasks'),
          );
        }
      } catch (_) {
        // error toast handled by API interceptor
      } finally {
        setLoading(false);
      }
    },
    [tr],
  );

  React.useEffect(() => {
    fetch(1, { mjId: '', start: '', end: '' });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const search = () => fetch(1, { mjId, start, end });
  const clear = () => {
    setMjId('');
    setStart('');
    setEnd('');
    fetch(1, { mjId: '', start: '', end: '' });
  };
  const goPage = (next) => fetch(next, { mjId, start, end });

  return (
    <>
      <div
        style={{
          padding: '10px 28px',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
          flexWrap: 'wrap',
        }}
      >
        <input
          style={{ ...inputStyle, width: 160 }}
          data-testid='mj-mj-id-filter'
          placeholder={tr('console.tasks.ph_mj_id', 'mj id…')}
          value={mjId}
          onChange={(e) => setMjId(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && search()}
        />
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.start_time', 'start time')}
          value={start}
          onChange={(e) => setStart(e.target.value)}
        />
        <span className='muted' style={{ fontSize: 11 }}>
          →
        </span>
        <input
          style={{ ...inputStyle, width: 160 }}
          type='datetime-local'
          title={tr('console.log.end_time', 'end time')}
          value={end}
          onChange={(e) => setEnd(e.target.value)}
        />
        <button
          type='button'
          className='btn primary'
          data-testid='mj-search-btn'
          onClick={search}
        >
          {tr('console.common.search', 'search')}
        </button>
        <button
          type='button'
          className='btn ghost'
          data-testid='mj-clear-btn'
          onClick={clear}
        >
          {tr('console.tasks.clear', 'clear')}
        </button>
      </div>

      {loading && (
        <div
          className='muted mono'
          style={{ padding: '20px 28px', fontSize: 12 }}
        >
          {tr('console.common.loading', 'loading…')}
        </div>
      )}

      {!loading && items.length === 0 && (
        <div
          className='muted'
          style={{ padding: '30px 28px', fontSize: 12 }}
          data-testid='mj-empty'
        >
          {tr('console.tasks.empty_mj', 'No Midjourney jobs found.')}
        </div>
      )}

      {!loading && items.length > 0 && (
        <div className='hf-table-scroll' style={{ padding: '0 28px' }}>
          <table className='t' data-testid='mj-table'>
            <thead>
              <tr>
                <th>{tr('console.tasks.th_submitted', 'submitted')}</th>
                <th>{tr('console.tasks.th_duration', 'duration')}</th>
                <th>{tr('console.tasks.th_type', 'type')}</th>
                <th>{tr('console.tasks.th_mj_id', 'mj id')}</th>
                <th>{tr('console.tasks.th_status', 'status')}</th>
                <th>{tr('console.tasks.th_progress', 'progress')}</th>
                <th>{tr('console.tasks.th_cost', 'cost')}</th>
                <th>{tr('console.tasks.th_result', 'result')}</th>
                <th>{tr('console.tasks.th_prompt', 'prompt')}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => {
                const dur = mjDurationSec(row.submit_time, row.finish_time);
                return (
                  <tr key={row.id} data-testid={`mj-row-${row.id}`}>
                    <td className='mono' style={cellStyle}>
                      {fmtMjTime(row.submit_time)}
                    </td>
                    <td className='mono' style={cellStyle}>
                      {dur != null ? `${dur.toFixed(1)}s` : '—'}
                    </td>
                    <td className='mono' style={cellStyle}>
                      {row.action || '—'}
                    </td>
                    <td
                      className='mono'
                      style={{ ...cellStyle, maxWidth: 160 }}
                      title={row.mj_id}
                    >
                      {row.mj_id || '—'}
                    </td>
                    <td style={cellStyle}>
                      <StatusTag status={row.status} />
                    </td>
                    <td style={cellStyle}>
                      <ProgressCell
                        value={row.progress}
                        failed={row.status === 'FAILURE'}
                      />
                    </td>
                    <td className='mono' style={cellStyle}>
                      {formatUSD(row.quota)}
                    </td>
                    <td style={cellStyle}>
                      {row.image_url ? (
                        <a
                          href={row.image_url}
                          target='_blank'
                          rel='noreferrer'
                        >
                          {tr('console.tasks.view_image', 'view image')}
                        </a>
                      ) : (
                        <span className='muted'>—</span>
                      )}
                    </td>
                    <td
                      style={{ ...cellStyle, maxWidth: 220 }}
                      title={row.prompt || ''}
                    >
                      <span className='muted'>{row.prompt || '—'}</span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 12,
          padding: '10px 28px',
          fontSize: 12,
        }}
      >
        <button
          type='button'
          className='btn ghost sm'
          disabled={page <= 1 || loading}
          onClick={() => goPage(page - 1)}
          data-testid='mj-prev-btn'
        >
          {tr('console.common.prev', '← prev')}
        </button>
        <span className='mono muted' data-testid='mj-range-label'>
          {tr(
            'console.tasks.range_of_total',
            '{{start}}–{{end}} of {{total}}',
            {
              start: total === 0 ? 0 : (page - 1) * PAGE_SIZE + 1,
              end: Math.min(page * PAGE_SIZE, total),
              total,
            },
          )}
        </span>
        <button
          type='button'
          className='btn ghost sm'
          disabled={page >= totalPages || loading}
          onClick={() => goPage(page + 1)}
          data-testid='mj-next-btn'
        >
          {tr('console.common.next', 'next →')}
        </button>
      </div>
    </>
  );
};

// ─── Page shell ─────────────────────────────────────────────────────────────

const HFTasks = () => {
  const { t: tr } = useTranslation();
  const [tab, setTab] = useState('tasks');

  return (
    <HFShell
      active='mj-logs'
      crumbs={[
        tr('console.nav.section_my_account', 'my account'),
        tr('console.tasks.crumb', 'async tasks'),
      ]}
    >
      <div
        style={{
          display: 'flex',
          padding: '0 28px',
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
        }}
      >
        {[
          ['tasks', tr('console.tasks.tab_tasks', 'Tasks')],
          ['mj', tr('console.tasks.tab_mj', 'Midjourney')],
        ].map(([k, l]) => (
          <button
            key={k}
            type='button'
            data-testid={`tasks-tab-${k}`}
            onClick={() => setTab(k)}
            style={{
              padding: '12px 16px',
              background: 'transparent',
              border: 0,
              cursor: 'pointer',
              fontFamily: 'var(--hf-mono)',
              fontSize: 11,
              color: tab === k ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
              borderBottom:
                tab === k
                  ? '2px solid var(--hf-accent)'
                  : '2px solid transparent',
              marginBottom: -1,
            }}
          >
            {l}
          </button>
        ))}
      </div>

      {tab === 'tasks' ? <TasksTab tr={tr} /> : <MjTab tr={tr} />}
    </HFShell>
  );
};

export default HFTasks;
