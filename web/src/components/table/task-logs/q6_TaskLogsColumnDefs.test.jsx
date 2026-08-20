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

// Task logs are what a customer opens to ask "what did this job do and why did
// it cost me that". The columns worth guarding hard:
//   * the CHANNEL column, which is admin-only — it names the upstream vendor;
//   * the task-id cell, which dumps the ENTIRE raw record into a modal for any
//     user, admin or not;
//   * status / type / duration, which are the only explanation a failed job
//     ever gives.
//
// Semi UI and lucide are stubbed to markers that carry the deciding props as
// attributes; nothing is stubbed to `() => null`.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';

vi.mock('@douyinfe/semi-ui', () => {
  // prefixIcon carries the type/status marker, so it is RENDERED rather than
  // swallowed — otherwise every branch would look identical in the DOM.
  const Tag = ({
    children,
    color,
    prefixIcon,
    shape,
    size,
    onClick,
    ...rest
  }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': String(color),
        'data-shape': shape,
        onClick,
        role: onClick ? 'button' : undefined,
        ...rest,
      },
      prefixIcon,
      children,
    );

  const Text = ({ children, onClick, ellipsis, style, ...rest }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'ellipsis-text',
        'data-tooltip': String(Boolean(ellipsis?.showTooltip)),
        onClick,
        role: onClick ? 'button' : undefined,
        ...rest,
      },
      children,
    );

  const Progress = ({ percent, stroke, showInfo }) =>
    React.createElement('div', {
      'data-testid': 'progress',
      'data-percent': String(percent),
      'data-stroke': String(stroke),
      'data-showinfo': String(showInfo),
    });

  return { Tag, Progress, Typography: { Text, Title: Text, Paragraph: Text } };
});

vi.mock('lucide-react', () => {
  const icon = (name) => (props) =>
    React.createElement('i', { 'data-testid': `icon-${name}`, ...props });
  return {
    Music: icon('music'),
    FileText: icon('filetext'),
    HelpCircle: icon('help'),
    CheckCircle: icon('check'),
    Pause: icon('pause'),
    Clock: icon('clock'),
    Play: icon('play'),
    XCircle: icon('x'),
    Loader: icon('loader'),
    List: icon('list'),
    Hash: icon('hash'),
    Video: icon('video'),
    Sparkles: icon('sparkles'),
  };
});

import { getTaskLogsColumns } from './TaskLogsColumnDefs';
import {
  TASK_ACTION_GENERATE,
  TASK_ACTION_TEXT_GENERATE,
  TASK_ACTION_FIRST_TAIL_GENERATE,
  TASK_ACTION_REFERENCE_GENERATE,
  TASK_ACTION_REMIX_GENERATE,
} from '../../../constants/common.constant';

const t = (k) => k;

const COLUMN_KEYS = {
  SUBMIT_TIME: 'submit_time',
  FINISH_TIME: 'finish_time',
  DURATION: 'duration',
  CHANNEL: 'channel',
  PLATFORM: 'platform',
  TYPE: 'type',
  TASK_ID: 'task_id',
  TASK_STATUS: 'task_status',
  PROGRESS: 'progress',
  FAIL_REASON: 'fail_reason',
};

const H = {
  copyText: vi.fn(),
  openContentModal: vi.fn(),
  openVideoModal: vi.fn(),
};

const cols = (over = {}) =>
  getTaskLogsColumns({
    t,
    COLUMN_KEYS,
    copyText: H.copyText,
    openContentModal: H.openContentModal,
    openVideoModal: H.openVideoModal,
    isAdminUser: true,
    ...over,
  });

const byKey = (key, over = {}) => cols(over).find((c) => c.key === key);

// Renders one cell. `record[dataIndex]` is used as the cell text so a render
// that ignores its first argument and reaches into the record still behaves,
// and vice versa.
const cell = (key, record, over = {}) => {
  const col = byKey(key, over);
  return render(
    React.createElement(
      'div',
      { 'data-testid': 'cell' },
      col.render(record[col.dataIndex], record, 0),
    ),
  );
};

// Submitted 2024-01-02 03:04:05 local, finished 30s later.
const SUBMIT = Math.floor(new Date(2024, 0, 2, 3, 4, 5).getTime() / 1000);

const baseRecord = (over = {}) => ({
  id: 9001,
  task_id: 'task-abc-123',
  channel_id: 37,
  platform: 'suno',
  action: 'MUSIC',
  status: 'SUCCESS',
  progress: '100%',
  submit_time: SUBMIT,
  finish_time: SUBMIT + 30,
  fail_reason: '',
  ...over,
});

beforeEach(() => vi.clearAllMocks());

describe('column set', () => {
  it('exposes the ten columns the screen renders, keyed by the caller’s map', () => {
    expect(cols().map((c) => c.key)).toEqual([
      'submit_time',
      'finish_time',
      'duration',
      'channel',
      'platform',
      'type',
      'task_id',
      'task_status',
      'progress',
      'fail_reason',
    ]);
  });

  it('pins only the details column to the right', () => {
    const fixed = cols().filter((c) => c.fixed);
    expect(fixed).toHaveLength(1);
    expect(fixed[0].key).toBe('fail_reason');
    expect(fixed[0].fixed).toBe('right');
  });

  it('reads the duration from finish_time but the submit time from its own field', () => {
    // Both time columns and the duration column share fields; a copy-paste
    // slip between them would swap what the customer is shown.
    expect(byKey('submit_time').dataIndex).toBe('submit_time');
    expect(byKey('finish_time').dataIndex).toBe('finish_time');
    expect(byKey('duration').dataIndex).toBe('finish_time');
  });
});

describe('time columns', () => {
  it('formats the submit time as a local date-time, to the second', () => {
    cell('submit_time', baseRecord());
    expect(screen.getByTestId('cell')).toHaveTextContent('2024-01-02 03:04:05');
  });

  it('formats the finish time from the finish field, not the submit field', () => {
    cell('finish_time', baseRecord({ finish_time: SUBMIT + 3600 }));
    expect(screen.getByTestId('cell')).toHaveTextContent('2024-01-02 04:04:05');
  });

  it('shows a dash rather than 1970 for a job that has not finished', () => {
    cell('finish_time', baseRecord({ finish_time: 0 }));
    expect(screen.getByTestId('cell')).toHaveTextContent('-');
    expect(screen.getByTestId('cell').textContent).not.toContain('1970');
  });

  it('shows a dash rather than 1970 for a job with no submit time', () => {
    cell('submit_time', baseRecord({ submit_time: 0 }));
    expect(screen.getByTestId('cell').textContent).not.toContain('1970');
  });
});

describe('duration column', () => {
  it('reports the elapsed seconds between submit and finish', () => {
    cell('duration', baseRecord({ finish_time: SUBMIT + 30 }));
    expect(screen.getByTestId('tag')).toHaveTextContent('30 秒');
  });

  it('stays green for a quick job and turns red once it runs long', () => {
    const { unmount } = render(
      React.createElement(
        'div',
        null,
        byKey('duration').render(
          SUBMIT + 60,
          baseRecord({ finish_time: SUBMIT + 60 }),
          0,
        ),
      ),
    );
    // 60s is the boundary and must still read as fast.
    expect(screen.getByTestId('tag')).toHaveAttribute('data-color', 'green');
    unmount();
    cell('duration', baseRecord({ finish_time: SUBMIT + 61 }));
    expect(screen.getByTestId('tag')).toHaveAttribute('data-color', 'red');
  });

  it('shows a dash for a job that has not finished yet', () => {
    cell('duration', baseRecord({ finish_time: 0 }));
    expect(screen.getByTestId('cell')).toHaveTextContent('-');
    expect(screen.queryByTestId('tag')).not.toBeInTheDocument();
  });

  it('says N/A rather than a bogus figure when the submit time is missing', () => {
    cell('duration', baseRecord({ submit_time: 0, finish_time: SUBMIT + 30 }));
    expect(screen.getByTestId('cell')).toHaveTextContent('N/A');
  });

  // DEFECT (correctness): the duration is a bare subtraction with no floor, so
  // a finish_time earlier than submit_time — clock skew between the gateway
  // and the upstream vendor, which is exactly what produces a support ticket —
  // renders as a NEGATIVE number of seconds, and the "> 60" colour test makes
  // it green, i.e. it is presented as a fast, healthy job.
  it.skip('CONTRACT: a job can never be shown as having taken negative time', () => {
    cell('duration', baseRecord({ finish_time: SUBMIT - 5 }));
    expect(screen.getByTestId('tag').textContent).not.toContain('-5');
  });

  it('currently subtracts the timestamps without a floor', () => {
    // Fix-safe: asserts the invariant a fix must preserve — the cell renders
    // SOMETHING for a skewed pair instead of blowing up — without pinning the
    // negative literal that the fix will remove.
    cell('duration', baseRecord({ finish_time: SUBMIT - 5 }));
    expect(screen.getByTestId('cell').textContent.length).toBeGreaterThan(0);
    expect(screen.getByTestId('cell').textContent).not.toContain('NaN');
  });
});

describe('channel column — admin only', () => {
  it('shows the channel id to an admin, and copies it when clicked', () => {
    cell('channel', baseRecord(), { isAdminUser: true });
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent('37');
    fireEvent.click(tag);
    // The id, not the index and not the whole record.
    expect(H.copyText).toHaveBeenCalledWith(37);
  });

  it('renders NOTHING for a non-admin — the upstream vendor is not their business', () => {
    cell('channel', baseRecord(), { isAdminUser: false });
    expect(screen.queryByTestId('tag')).not.toBeInTheDocument();
    expect(screen.getByTestId('cell').textContent).toBe('');
  });

  it('gives different channels different colours', () => {
    // A single hard-coded colour would pass a one-channel fixture; two
    // channels whose ids differ mod the palette length prove the map is live.
    const { unmount } = render(
      React.createElement(
        'div',
        null,
        byKey('channel').render(1, baseRecord({ channel_id: 1 }), 0),
      ),
    );
    const first = screen.getByTestId('tag').getAttribute('data-color');
    unmount();
    render(
      React.createElement(
        'div',
        null,
        byKey('channel').render(2, baseRecord({ channel_id: 2 }), 0),
      ),
    );
    expect(screen.getByTestId('tag').getAttribute('data-color')).not.toBe(
      first,
    );
  });
});

describe('platform column', () => {
  it('names a known numeric channel type from the shared option list', () => {
    cell('platform', baseRecord({ platform: 1 }));
    expect(screen.getByTestId('tag')).toHaveTextContent('OpenAI');
  });

  it('matches a numeric platform sent as a string', () => {
    cell('platform', baseRecord({ platform: '1' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('OpenAI');
  });

  it('special-cases suno', () => {
    cell('platform', baseRecord({ platform: 'suno' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('Suno');
  });

  it('says 未知 for a platform the console does not model', () => {
    cell('platform', baseRecord({ platform: 'brand-new-vendor' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('未知');
    expect(screen.getByTestId('icon-help')).toBeInTheDocument();
  });
});

describe('type column', () => {
  const cases = [
    ['MUSIC', '生成音乐'],
    ['LYRICS', '生成歌词'],
    [TASK_ACTION_GENERATE, '图生视频'],
    [TASK_ACTION_TEXT_GENERATE, '文生视频'],
    [TASK_ACTION_FIRST_TAIL_GENERATE, '首尾生视频'],
    [TASK_ACTION_REFERENCE_GENERATE, '参照生视频'],
    [TASK_ACTION_REMIX_GENERATE, '视频Remix'],
  ];

  cases.forEach(([action, label]) => {
    it(`labels ${action} as ${label}`, () => {
      cell('type', baseRecord({ action }));
      expect(screen.getByTestId('tag')).toHaveTextContent(label);
    });
  });

  it('says 未知 for an action the console does not model', () => {
    cell('type', baseRecord({ action: 'somethingNew' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('未知');
  });

  it('does not label an unknown action as any of the known types', () => {
    // Negative half: without this, a default branch that happened to return
    // 图生视频 would satisfy every positive case above.
    cell('type', baseRecord({ action: 'somethingNew' }));
    const text = screen.getByTestId('tag').textContent;
    cases.forEach(([, label]) => expect(text).not.toContain(label));
  });
});

describe('status column', () => {
  const cases = [
    ['SUCCESS', '成功', 'green'],
    ['NOT_START', '未启动', 'grey'],
    ['SUBMITTED', '队列中', 'yellow'],
    ['IN_PROGRESS', '执行中', 'blue'],
    ['FAILURE', '失败', 'red'],
    ['QUEUED', '排队中', 'orange'],
    ['UNKNOWN', '未知', 'white'],
    ['', '正在提交', 'grey'],
  ];

  cases.forEach(([status, label, color]) => {
    it(`shows ${label} for status "${status}"`, () => {
      cell('task_status', baseRecord({ status }));
      expect(screen.getByTestId('tag')).toHaveTextContent(label);
      expect(screen.getByTestId('tag')).toHaveAttribute('data-color', color);
    });
  });

  it('never shows a failed job as successful', () => {
    cell('task_status', baseRecord({ status: 'FAILURE' }));
    const tag = screen.getByTestId('tag');
    expect(tag.textContent).not.toContain('成功');
    expect(tag).toHaveAttribute('data-color', 'red');
  });

  it('falls back to 未知 for a status the console does not model', () => {
    cell('task_status', baseRecord({ status: 'PARTIALLY_REFUNDED' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('未知');
  });
});

describe('progress column', () => {
  it('draws a bar at the reported percentage', () => {
    cell('progress', baseRecord({ progress: '45%' }));
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-percent',
      '45',
    );
  });

  it('draws 0% — not 100% — for a job with no progress reported', () => {
    cell('progress', baseRecord({ progress: '0%' }));
    expect(screen.getByTestId('progress')).toHaveAttribute('data-percent', '0');
  });

  it('prints the raw text instead of a bar when the value is not a percentage', () => {
    cell('progress', baseRecord({ progress: 'pending' }));
    expect(screen.queryByTestId('progress')).not.toBeInTheDocument();
    expect(screen.getByTestId('cell')).toHaveTextContent('pending');
  });

  it('shows a dash when progress is absent altogether', () => {
    cell('progress', baseRecord({ progress: undefined }));
    expect(screen.queryByTestId('progress')).not.toBeInTheDocument();
    expect(screen.getByTestId('cell')).toHaveTextContent('-');
  });

  it('recolours the bar for a failed job', () => {
    const { unmount } = render(
      React.createElement(
        'div',
        null,
        byKey('progress').render(
          '80%',
          baseRecord({ progress: '80%', status: 'FAILURE' }),
          0,
        ),
      ),
    );
    const failed = screen.getByTestId('progress').getAttribute('data-stroke');
    unmount();
    cell('progress', baseRecord({ progress: '80%', status: 'IN_PROGRESS' }));
    // Positive AND negative: a running job must NOT get the failure stroke, or
    // "recolours on failure" would be indistinguishable from "always red".
    expect(screen.getByTestId('progress').getAttribute('data-stroke')).not.toBe(
      failed,
    );
    expect(failed).toContain('warning');
  });
});

describe('task id cell — the raw record dump', () => {
  it('shows the task id with a tooltip for the truncated text', () => {
    cell('task_id', baseRecord());
    expect(screen.getByTestId('ellipsis-text')).toHaveTextContent(
      'task-abc-123',
    );
    expect(screen.getByTestId('ellipsis-text')).toHaveAttribute(
      'data-tooltip',
      'true',
    );
  });

  it('opens the detail modal with the record that was clicked', () => {
    cell('task_id', baseRecord({ task_id: 'task-zzz-999' }));
    fireEvent.click(screen.getByTestId('ellipsis-text'));
    expect(H.openContentModal).toHaveBeenCalledTimes(1);
    const payload = H.openContentModal.mock.calls[0][0];
    // The clicked row, not a neighbour and not a stale one.
    expect(payload).toContain('task-zzz-999');
  });

  // DEFECT (security): the channel column is deliberately hidden from
  // non-admins — it names the upstream vendor and its internal id. The task-id
  // cell, however, serialises the WHOLE record with JSON.stringify and hands it
  // to the content modal for EVERY user, admin or not. So a normal customer
  // clicking a task id sees channel_id, and any other internal field the API
  // happens to return on that row, in full. The dump needs the same admin gate
  // (or a field allow-list) the column next to it already has.
  it.skip('CONTRACT: the raw-record dump must not leak fields hidden from non-admins', () => {
    cell('task_id', baseRecord(), { isAdminUser: false });
    fireEvent.click(screen.getByTestId('ellipsis-text'));
    expect(H.openContentModal.mock.calls[0][0]).not.toContain('channel_id');
  });

  it('currently builds the same dump for a non-admin as for an admin', () => {
    // Fix-safe: asserts that a non-admin can still open the detail modal and
    // still sees the task id — the useful part, which any fix keeps. It does
    // NOT assert that channel_id is present, which would turn red the moment
    // the leak is closed.
    cell('task_id', baseRecord(), { isAdminUser: false });
    fireEvent.click(screen.getByTestId('ellipsis-text'));
    expect(H.openContentModal).toHaveBeenCalledTimes(1);
    expect(H.openContentModal.mock.calls[0][0]).toContain('task-abc-123');
  });
});

describe('details column', () => {
  const VIDEO_URL = 'https://cdn.example.com/out/9001.mp4';

  it('says 无 when there is nothing to report', () => {
    cell('fail_reason', baseRecord({ fail_reason: '' }));
    expect(screen.getByTestId('cell')).toHaveTextContent('无');
  });

  it('shows the failure text and opens it in full when clicked', () => {
    cell(
      'fail_reason',
      baseRecord({
        status: 'FAILURE',
        fail_reason: 'upstream 429 rate limited',
      }),
    );
    const text = screen.getByTestId('ellipsis-text');
    expect(text).toHaveTextContent('upstream 429 rate limited');
    fireEvent.click(text);
    expect(H.openContentModal).toHaveBeenCalledWith(
      'upstream 429 rate limited',
    );
  });

  it('offers a video preview only for a successful video job with a URL', () => {
    cell(
      'fail_reason',
      baseRecord({
        action: TASK_ACTION_TEXT_GENERATE,
        status: 'SUCCESS',
        fail_reason: VIDEO_URL,
      }),
    );
    const link = screen.getByText('点击预览视频');
    fireEvent.click(link);
    expect(H.openVideoModal).toHaveBeenCalledWith(VIDEO_URL);
    expect(H.openContentModal).not.toHaveBeenCalled();
  });

  it('does NOT offer a preview for a FAILED video job that happens to hold a URL', () => {
    cell(
      'fail_reason',
      baseRecord({
        action: TASK_ACTION_TEXT_GENERATE,
        status: 'FAILURE',
        fail_reason: VIDEO_URL,
      }),
    );
    expect(screen.queryByText('点击预览视频')).not.toBeInTheDocument();
    expect(H.openVideoModal).not.toHaveBeenCalled();
  });

  it('does NOT offer a preview for a successful MUSIC job holding a URL', () => {
    cell(
      'fail_reason',
      baseRecord({
        action: 'MUSIC',
        status: 'SUCCESS',
        fail_reason: VIDEO_URL,
      }),
    );
    expect(screen.queryByText('点击预览视频')).not.toBeInTheDocument();
  });

  it('does NOT treat a non-URL detail on a successful video job as a preview', () => {
    cell(
      'fail_reason',
      baseRecord({
        action: TASK_ACTION_GENERATE,
        status: 'SUCCESS',
        fail_reason: 'finished in 12s',
      }),
    );
    expect(screen.queryByText('点击预览视频')).not.toBeInTheDocument();
    expect(screen.getByTestId('ellipsis-text')).toHaveTextContent(
      'finished in 12s',
    );
  });

  it('does not treat a javascript: payload as a previewable video URL', () => {
    // Only http(s) reaches openVideoModal; anything else falls through to the
    // plain text branch. Asserting this keeps the URL test from being widened
    // to a bare "contains ://" check later.
    cell(
      'fail_reason',
      baseRecord({
        action: TASK_ACTION_GENERATE,
        status: 'SUCCESS',
        // eslint-disable-next-line no-script-url
        fail_reason: 'javascript:alert(1)',
      }),
    );
    expect(screen.queryByText('点击预览视频')).not.toBeInTheDocument();
    expect(H.openVideoModal).not.toHaveBeenCalled();
  });
});
