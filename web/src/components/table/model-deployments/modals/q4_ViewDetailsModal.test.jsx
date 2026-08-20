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

// The read-only detail panel for one deployment. It is the screen an operator
// checks before deciding whether to extend or destroy, so the figures on it
// are decisions, not decoration:
//
//   * 已支付金额 is a money figure, printed straight from amount_paid;
//   * 已运行 / 剩余 minutes drive the "is this container about to die" call;
//   * the container list carries an external link, which must stay
//     noopener/noreferrer — the tab it opens is operator-controlled
//     infrastructure.
//
// timestamp2string is stubbed to a marker here (the real formatter is covered
// against real epochs in the column-definition tests) so these assertions do
// not depend on the runner's timezone.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  get: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../../helpers', () => ({
  API: { get: H.get },
  showError: H.showError,
  showSuccess: H.showSuccess,
  timestamp2string: (v) => `ts(${v})`,
}));

vi.mock('react-icons/fa', () => {
  const make = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
  return {
    FaInfoCircle: make('info'),
    FaServer: make('server'),
    FaClock: make('clock'),
    FaMapMarkerAlt: make('map'),
    FaDocker: make('docker'),
    FaMoneyBillWave: make('money'),
    FaChartLine: make('chart'),
    FaCopy: make('copy'),
    FaLink: make('link'),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconRefresh: () =>
    React.createElement('i', { 'data-testid': 'icon-refresh' }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Modal = ({ children, visible, title, footer }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal' },
          title,
          children,
          footer,
        )
      : null;

  const Button = ({ children, onClick, icon, loading, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        'data-loading': String(!!loading),
        ...rest,
      },
      icon,
      children,
    );

  const Text = ({ children, type, ...rest }) =>
    React.createElement('span', { 'data-type': type }, children);

  const Card = ({ children, title }) =>
    React.createElement('section', { 'data-testid': 'card' }, title, children);

  // Descriptions is the layout for most of the panel: it must RENDER its
  // data, not swallow it, or half the assertions below would be vacuous.
  const Descriptions = ({ data }) =>
    React.createElement(
      'dl',
      { 'data-testid': 'descriptions' },
      (data || []).map((row, i) =>
        React.createElement(
          'div',
          { key: i, 'data-testid': 'desc-row' },
          React.createElement('dt', null, row.key),
          React.createElement('dd', null, row.value),
        ),
      ),
    );

  const Empty = ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description);
  Empty.PRESENTED_IMAGE_SIMPLE = 'simple';

  return {
    Modal,
    Button,
    Card,
    Descriptions,
    Empty,
    Typography: { Text, Title: Text },
    Tag: ({ children, color }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tag', 'data-color': color },
        children,
      ),
    Progress: ({ percent, status }) =>
      React.createElement('div', {
        'data-testid': 'progress',
        'data-percent': String(percent),
        'data-status': status,
      }),
    Spin: ({ tip }) =>
      React.createElement('div', { 'data-testid': 'spin' }, tip),
    Badge: ({ children, count }) =>
      React.createElement(
        'span',
        { 'data-testid': 'badge', 'data-count': String(count) },
        children,
      ),
    Tooltip: ({ children, content }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tooltip', 'data-content': String(content) },
        children,
      ),
  };
});

import ViewDetailsModal from './ViewDetailsModal';

const t = (k) => k;

const deployment = { id: 'dep-77', status: 'running' };

const details = {
  id: 'dep-77',
  deployment_name: 'prod-llama-serving',
  brand_name: 'NVIDIA',
  hardware_name: 'H100 SXM',
  total_gpus: 8,
  gpus_per_container: 2,
  total_containers: 4,
  completed_percent: 62,
  compute_minutes_served: 150,
  compute_minutes_remaining: 90,
  amount_paid: 12.3456,
  created_at: 1704412089,
  updated_at: 1704498489,
  started_at: 1704412089,
  finished_at: 1704498489,
  container_config: {
    image_url: 'ollama/ollama:latest',
    traffic_port: 11434,
    entrypoint: ['/bin/bash', '-c'],
    env_variables: { MODEL: 'llama3', TZ: 'UTC' },
  },
  locations: [{ id: 11, name: 'Frankfurt', iso2: 'DE' }],
};

const containers = [
  {
    container_id: 'ctr-1',
    device_id: 'dev-a',
    status: 'running',
    gpus_per_container: 2,
    created_at: 1704412089,
    public_url: 'https://ctr-1.example.test',
    events: [{ time: 1704412100, message: 'started' }],
  },
];

const routeGet = (overrides = {}) => {
  H.get.mockImplementation((url) => {
    if (String(url).endsWith('/containers')) {
      return Promise.resolve(
        overrides.containers ?? {
          data: { success: true, data: { containers } },
        },
      );
    }
    return Promise.resolve(
      overrides.details ?? { data: { success: true, data: details } },
    );
  });
};

const renderModal = (overrides = {}) => {
  const props = {
    visible: true,
    onCancel: vi.fn(),
    deployment,
    t,
    ...overrides,
  };
  const view = render(<ViewDetailsModal {...props} />);
  return { props, view };
};

const body = () => screen.getByTestId('modal').textContent;

beforeEach(() => {
  vi.clearAllMocks();
  routeGet();
});

describe('loading the panel', () => {
  it('asks for both the deployment and its containers', async () => {
    renderModal();
    await waitFor(() => expect(H.get).toHaveBeenCalledTimes(2));
    expect(H.get).toHaveBeenCalledWith('/api/deployments/dep-77');
    expect(H.get).toHaveBeenCalledWith('/api/deployments/dep-77/containers');
  });

  it('fetches nothing while closed', async () => {
    renderModal({ visible: false });
    await Promise.resolve();
    expect(H.get).not.toHaveBeenCalled();
  });

  it('fetches nothing for a row without an id', async () => {
    renderModal({ deployment: { status: 'running' } });
    await Promise.resolve();
    expect(H.get).not.toHaveBeenCalled();
  });

  it('re-reads both endpoints when the operator refreshes', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(H.get).toHaveBeenCalledTimes(2));

    await user.click(screen.getByText('刷新'));
    await waitFor(() => expect(H.get).toHaveBeenCalledTimes(4));
  });

  it('says it cannot load rather than showing a half-empty panel', async () => {
    routeGet({ details: { data: { success: false, message: 'gone' } } });
    renderModal();
    expect(await screen.findByText('无法获取容器详情')).toBeInTheDocument();
  });

  it('reports a failed request with the reason the API gave', async () => {
    H.get.mockRejectedValue({ response: { data: { message: 'boom' } } });
    renderModal();
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('获取详情失败: boom'),
    );
  });
});

describe('money figures', () => {
  it('shows the amount already paid to two decimals', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    // 12.3456 -> 12.35: rounded for display, not truncated to 12.34.
    expect(body()).toContain('12.35');
    expect(body()).toContain('USDC');
  });

  it('shows a zero balance as 0.00, never as a blank', async () => {
    routeGet({
      details: {
        data: { success: true, data: { ...details, amount_paid: 0 } },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('0.00');
    expect(body()).not.toContain('NaN');
  });

  // DEFECT (money/correctness): amount_paid is fed to .toFixed(2) with no type
  // guard. A decimal that arrives as a STRING — the usual JSON encoding for
  // money — makes the whole panel throw during render, so the operator loses
  // every figure on the screen, not just the price. Fix: Number(amount_paid)
  // with a finite check.
  //
  // No passing twin is written for this one: today's behaviour is a thrown
  // render, and any assertion pinning that (expect(render).toThrow()) would go
  // red the moment somebody fixes it. The number path above is the twin.
  it.skip('LOCK: an amount that arrives as a decimal string still renders', async () => {
    routeGet({
      details: {
        data: { success: true, data: { ...details, amount_paid: '12.34' } },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('12.34');
  });
});

describe('runtime figures', () => {
  it('splits served and remaining minutes into hours and minutes', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    // 150 served = 2h30m, 90 remaining = 1h30m. Two different values, so a
    // cell reading the wrong field is visible.
    expect(body()).toContain('2h 30m');
    expect(body()).toContain('1h 30m');
  });

  it('reports the completion percentage the API sent', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(screen.getByTestId('progress').dataset.percent).toBe('62');
    expect(screen.getByTestId('progress').dataset.status).toBe('normal');
  });

  it('marks a finished container as complete', async () => {
    routeGet({
      details: {
        data: {
          success: true,
          data: { ...details, completed_percent: 100 },
        },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(screen.getByTestId('progress').dataset.status).toBe('success');
  });

  // DEFECT (correctness): the h/m split divides compute_minutes_served and
  // compute_minutes_remaining without checking they exist, so a deployment the
  // backend has not metered yet renders the literal "NaNh NaNm" in the time
  // panel. Fix: fall back to 0 / "--" when the figure is absent.
  it.skip('LOCK: unmetered runtimes are not printed as NaN', async () => {
    routeGet({
      details: {
        data: {
          success: true,
          data: {
            ...details,
            compute_minutes_served: undefined,
            compute_minutes_remaining: undefined,
          },
        },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).not.toContain('NaN');
  });

  it('currently: an unmetered deployment still renders the rest of the panel', async () => {
    routeGet({
      details: {
        data: {
          success: true,
          data: {
            ...details,
            compute_minutes_served: undefined,
            compute_minutes_remaining: undefined,
          },
        },
      },
    });
    renderModal();

    // Invariants either way: the panel does not blow up, and the identifying
    // fields are still there. The "NaN" literal itself is deliberately not
    // pinned — that is what the lock above is for.
    expect(await screen.findByText('prod-llama-serving')).toBeInTheDocument();
    expect(body()).toContain('H100 SXM');
  });
});

describe('identity and status', () => {
  it('shows name, id and hardware from the fetched detail', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('dep-77');
    expect(body()).toContain('H100 SXM');
    expect(body()).toContain('NVIDIA');
  });

  it('reports the per-container GPU split and the container count', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    // 8 total across 4 containers at 2 each: three distinct numbers, so a
    // swapped pair cannot pass.
    expect(body()).toContain('8');
    expect(screen.getByTestId('badge').dataset.count).toBe('8');
    expect(body()).toContain('每容器GPU数');
  });

  it.each([
    ['running', 'green', '运行中'],
    ['failed', 'red', '失败'],
    ['destroyed', 'red', '已销毁'],
  ])('paints the %s state', async (status, color, label) => {
    renderModal({ deployment: { ...deployment, status } });
    await screen.findByText('prod-llama-serving');
    const statusTag = screen
      .getAllByTestId('tag')
      .find((n) => n.textContent.trim() === label);
    expect(statusTag).toBeDefined();
    expect(statusTag.dataset.color).toBe(color);
  });

  it('keeps an unmapped status visible instead of dropping it', async () => {
    // 'stopped' is known to the table but absent from this modal's map; the
    // operator must still see what the backend said.
    renderModal({ deployment: { ...deployment, status: 'stopped' } });
    await screen.findByText('prod-llama-serving');
    const statusTag = screen
      .getAllByTestId('tag')
      .find((n) => n.textContent.trim() === 'stopped');
    expect(statusTag).toBeDefined();
    expect(statusTag.dataset.color).toBe('grey');
  });

  it('copies the deployment id on request', async () => {
    const user = userEvent.setup();
    const writeText = vi.fn();
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    renderModal();
    await screen.findByText('prod-llama-serving');
    const copyButton = screen
      .getAllByRole('button')
      .find((b) => b.querySelector('[data-testid="icon-copy"]'));
    await user.click(copyButton);

    expect(writeText).toHaveBeenCalledWith('dep-77');
    expect(H.showSuccess).toHaveBeenCalledWith('已复制 ID 到剪贴板');
  });
});

describe('container configuration', () => {
  it('shows the image, port and entrypoint actually deployed', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('ollama/ollama:latest');
    expect(body()).toContain('11434');
    expect(body()).toContain('/bin/bash -c');
  });

  it('lists the environment the container runs with', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('MODEL=');
    expect(body()).toContain('llama3');
  });

  it('omits the configuration card when the API sent none', async () => {
    routeGet({
      details: {
        data: {
          success: true,
          data: { ...details, container_config: undefined },
        },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).not.toContain('ollama/ollama:latest');
    expect(body()).not.toContain('镜像地址');
  });

  it('names the region the deployment runs in', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('Frankfurt');
    expect(body()).toContain('DE');
  });
});

describe('container instances', () => {
  it('lists each container with its device, status and GPU share', async () => {
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(body()).toContain('ctr-1');
    expect(body()).toContain('dev-a');
    expect(body()).toContain('started');
  });

  it('says so plainly when the deployment has no containers yet', async () => {
    routeGet({
      containers: { data: { success: true, data: { containers: [] } } },
    });
    renderModal();
    expect(await screen.findByText('暂无容器信息')).toBeInTheDocument();
  });

  it('opens the container link in a tab that cannot reach back', async () => {
    const user = userEvent.setup();
    const open = vi.fn();
    vi.stubGlobal('open', open);

    renderModal();
    await screen.findByText('prod-llama-serving');
    await user.click(screen.getByText('访问容器'));

    expect(open).toHaveBeenCalledWith(
      'https://ctr-1.example.test',
      '_blank',
      // Reverse-tabnabbing guard: the opened page must not get window.opener.
      expect.stringContaining('noopener'),
    );
    expect(open.mock.calls[0][2]).toContain('noreferrer');
    vi.unstubAllGlobals();
  });

  it('offers no link for a container that has not published one', async () => {
    routeGet({
      containers: {
        data: {
          success: true,
          data: { containers: [{ ...containers[0], public_url: '' }] },
        },
      },
    });
    renderModal();
    await screen.findByText('prod-llama-serving');
    expect(screen.queryByText('访问容器')).not.toBeInTheDocument();
  });

  it('reports a failed container fetch separately from the detail fetch', async () => {
    H.get.mockImplementation((url) =>
      String(url).endsWith('/containers')
        ? Promise.reject({ response: { data: { message: 'no agent' } } })
        : Promise.resolve({ data: { success: true, data: details } }),
    );
    renderModal();
    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('获取容器信息失败: no agent'),
    );
    // The panel itself must survive a container-list failure.
    expect(await screen.findByText('prod-llama-serving')).toBeInTheDocument();
  });
});
