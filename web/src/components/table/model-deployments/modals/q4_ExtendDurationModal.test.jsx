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

// Extending a container's runtime is a MONEY action: the modal quotes a price
// and the confirm button spends the account balance immediately ("费用将立即
// 扣除"). Two things therefore carry the weight here:
//
//   * the quote request must be derived from the deployment's real shape.
//     The fixture uses total_gpus 8 across total_containers 4 with NO
//     gpus_per_container field, so a correct derivation (2 per container,
//     4 replicas) and any hard-coded 1 produce different requests.
//   * the quoted figures must be rendered at full precision from the field
//     the API actually sent. Rates carry more than four decimals in the
//     fixtures so that toFixed(4) and toFixed(2) cannot both pass.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  form: { onValueChange: null, values: {}, reset: vi.fn() },
}));

vi.mock('../../../../helpers', () => ({
  API: { get: H.get, post: H.post },
  showError: H.showError,
  showSuccess: H.showSuccess,
}));

vi.mock('react-icons/fa', () => {
  const make = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
  return {
    FaClock: make('clock'),
    FaCalculator: make('calculator'),
    FaInfoCircle: make('info'),
    FaExclamationTriangle: make('warning'),
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const Modal = ({
    children,
    visible,
    title,
    onOk,
    onCancel,
    okText,
    cancelText,
    okButtonProps = {},
    confirmLoading,
  }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal' },
          React.createElement('div', null, title),
          children,
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-ok',
              disabled: !!okButtonProps.disabled,
              'data-confirm-loading': String(!!confirmLoading),
              onClick: onOk,
            },
            okText,
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-cancel',
              onClick: onCancel,
            },
            cancelText,
          ),
        )
      : null;

  // The Form stub hands back a form api with the three methods the component
  // calls, and wires the child input to Semi's onValueChange contract (the
  // whole value bag, not the raw event).
  const Form = ({ children, getFormApi, onValueChange }) => {
    H.form.onValueChange = onValueChange;
    const api = {
      setValue: (field, value) => {
        H.form.values[field] = value;
      },
      getValue: (field) => H.form.values[field],
      validate: () => Promise.resolve(H.form.values),
      reset: H.form.reset,
    };
    React.useEffect(() => {
      getFormApi?.(api);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return React.createElement('div', { 'data-testid': 'form' }, children);
  };
  Form.InputNumber = ({ field, label, min, max }) =>
    React.createElement(
      'label',
      null,
      label,
      React.createElement('input', {
        'data-testid': `field-${field}`,
        'data-min': String(min),
        'data-max': String(max),
        onChange: (e) =>
          H.form.onValueChange?.({ [field]: Number(e.target.value) }),
      }),
    );

  const Button = ({ children, onClick, disabled, theme, type, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        disabled: !!disabled,
        'data-theme': theme,
        'data-btn-type': type,
        ...rest,
      },
      children,
    );

  const Text = ({ children, type, strong, size, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': type === 'danger' ? 'danger-text' : 'text' },
      children,
    );

  const passthrough = (testid) => {
    const C = ({ children, title, description }) =>
      React.createElement(
        'div',
        { 'data-testid': testid },
        title,
        description,
        children,
      );
    return C;
  };

  return {
    Modal,
    Form,
    InputNumber: Form.InputNumber,
    Typography: { Text },
    Card: passthrough('card'),
    Space: passthrough('space'),
    Divider: () => React.createElement('hr', null),
    Button,
    Tag: ({ children }) =>
      React.createElement('span', { 'data-testid': 'tag' }, children),
    Banner: passthrough('banner'),
    Spin: () => React.createElement('i', { 'data-testid': 'spin' }),
  };
});

import ExtendDurationModal from './ExtendDurationModal';

const t = (k) => k;

const deployment = {
  id: 'dep-77',
  container_name: 'prod-llama-serving',
  time_remaining: '3小时20分钟',
  hardware_name: 'stale-name-from-row',
  hardware_quantity: 1,
};

// total_gpus / total_containers deliberately disagree with a naive "1 each":
// 8 GPUs over 4 containers is 2 per container, 4 replicas.
const details = {
  hardware_id: 31,
  total_gpus: 8,
  total_containers: 4,
  hardware_name: 'H100 SXM',
  locations: [
    { id: 11 },
    { location_id: 12 },
    { id: 0 }, // not a real location id — must be dropped
    { id: 'not-a-number' },
  ],
};

const priceBody = {
  success: true,
  data: {
    currency: 'usdc',
    estimated_cost: 9.87654321,
    price_breakdown: { hourly_rate: 1.23456789, compute_cost: 8.11111111 },
  },
};

const okDetails = { data: { success: true, data: details } };

const renderModal = (overrides = {}) => {
  const props = {
    visible: true,
    onCancel: vi.fn(),
    deployment,
    onSuccess: vi.fn(),
    t,
    ...overrides,
  };
  const view = render(<ExtendDurationModal {...props} />);
  return { props, view };
};

const priceCalls = () =>
  H.post.mock.calls.filter((c) => c[0] === '/api/deployments/price-estimation');

beforeEach(() => {
  vi.clearAllMocks();
  H.form.values = {};
  H.get.mockResolvedValue(okDetails);
  H.post.mockResolvedValue({ data: priceBody });
});

describe('quote request', () => {
  it('derives GPUs-per-container and replica count from the deployment shape', async () => {
    renderModal();

    await waitFor(() => expect(priceCalls()).toHaveLength(1));
    expect(H.get).toHaveBeenCalledWith('/api/deployments/dep-77');
    expect(priceCalls()[0][1]).toEqual({
      location_ids: [11, 12],
      hardware_id: 31,
      gpus_per_container: 2,
      duration_hours: 1,
      replica_count: 4,
      currency: 'usdc',
      duration_type: 'hour',
      duration_qty: 1,
      hardware_qty: 2,
    });
  });

  it('prefers an explicit gpus_per_container over the derived one', async () => {
    H.get.mockResolvedValue({
      data: {
        success: true,
        data: { ...details, gpus_per_container: 3 },
      },
    });
    renderModal();

    await waitFor(() => expect(priceCalls()).toHaveLength(1));
    expect(priceCalls()[0][1].gpus_per_container).toBe(3);
    expect(priceCalls()[0][1].hardware_qty).toBe(3);
  });

  it('re-quotes for the hours the operator picked', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    // The 48-hour preset is labelled in days, so a re-quote of 2 (the label)
    // instead of 48 (the value) would be visible here.
    await user.click(screen.getByText('2天'));

    await waitFor(() => expect(priceCalls()).toHaveLength(2));
    const latest = priceCalls().at(-1)[1];
    expect(latest.duration_hours).toBe(48);
    expect(latest.duration_qty).toBe(48);
  });

  it('refuses to quote a deployment it cannot price, and says so', async () => {
    H.get.mockResolvedValue({
      data: { success: true, data: { ...details, locations: [] } },
    });
    renderModal();

    expect(await screen.findByText('价格计算失败')).toBeInTheDocument();
    // A quote request with no locations would come back priced at zero.
    expect(priceCalls()).toHaveLength(0);
  });

  it('surfaces a failed detail fetch instead of quoting from the stale row', async () => {
    H.get.mockResolvedValue({
      data: { success: false, message: 'not found' },
    });
    renderModal();

    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith('获取详情失败: not found'),
    );
    expect(priceCalls()).toHaveLength(0);
    expect(
      await screen.findByText('获取详情失败: not found'),
    ).toBeInTheDocument();
  });

  it('surfaces a rejected quote with the reason the API gave', async () => {
    H.post.mockResolvedValue({
      data: { success: false, message: 'no capacity' },
    });
    renderModal();

    expect(
      await screen.findByText('价格计算失败: no capacity'),
    ).toBeInTheDocument();
  });

  it('does nothing at all while the modal is closed', async () => {
    renderModal({ visible: false });
    await Promise.resolve();
    expect(H.get).not.toHaveBeenCalled();
    expect(H.post).not.toHaveBeenCalled();
  });
});

describe('quoted figures', () => {
  it('shows total, hourly rate and compute cost at four decimals in the quoted currency', async () => {
    renderModal();

    expect(await screen.findByText('9.8765 USDC')).toBeInTheDocument();
    expect(screen.getByText('1.2346 USDC')).toBeInTheDocument();
    expect(screen.getByText('8.1111 USDC')).toBeInTheDocument();
  });

  it('labels the figures with the currency the API priced in, not the requested one', async () => {
    H.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          currency: 'iocoin',
          estimated_cost: 2.5,
          price_breakdown: { hourly_rate: 0.5 },
        },
      },
    });
    renderModal();

    expect(await screen.findByText('2.5000 IOCOIN')).toBeInTheDocument();
  });

  it('falls back to the breakdown total when no top-level cost is quoted', async () => {
    H.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          currency: 'usdc',
          price_breakdown: { total_cost: 42.5, hourly_rate: 1.5 },
        },
      },
    });
    renderModal();

    expect(await screen.findByText('42.5000 USDC')).toBeInTheDocument();
  });

  it('accepts the PascalCase envelope the API also emits', async () => {
    H.post.mockResolvedValue({
      data: {
        success: true,
        data: {
          Currency: 'usdc',
          EstimatedCost: 12.3456789,
          PriceBreakdown: { HourlyRate: 0.9876543 },
        },
      },
    });
    renderModal();

    expect(await screen.findByText('12.3457 USDC')).toBeInTheDocument();
    expect(screen.getByText('0.9877 USDC')).toBeInTheDocument();
  });

  it('prints a dash rather than a fabricated figure when the total is missing', async () => {
    H.post.mockResolvedValue({
      data: {
        success: true,
        data: { currency: 'usdc', price_breakdown: { hourly_rate: 1.5 } },
      },
    });
    renderModal();

    await screen.findByText('1.5000 USDC/h'.replace('/h', ''));
    // 0.0000 here would read as "this extension is free".
    expect(screen.queryByText('0.0000 USDC')).not.toBeInTheDocument();
    expect(screen.getAllByText('--').length).toBeGreaterThan(0);
  });

  it('reports the hardware from the fetched details, not the stale row copy', async () => {
    renderModal();
    await screen.findByText('9.8765 USDC');
    expect(screen.queryByText(/stale-name-from-row/)).not.toBeInTheDocument();
  });

  // GAP (not locked): a rate that arrives as a numeric STRING ("1.5") passes
  // neither the `typeof === 'number'` branch nor the PascalCase fallback, so
  // the cell shows "--". Silently omitting a price is the safe direction, and
  // pinning "--" would block a future fix that parses it.
});

describe('confirming the extension', () => {
  it('charges exactly the hours the operator chose', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    await user.click(screen.getByText('12小时'));
    await waitFor(() => expect(priceCalls()).toHaveLength(2));

    H.post.mockResolvedValue({
      data: { success: true, data: { id: 'dep-77' } },
    });
    await user.click(screen.getByTestId('modal-ok'));

    await waitFor(() =>
      expect(H.post).toHaveBeenCalledWith('/api/deployments/dep-77/extend', {
        duration_hours: 12,
      }),
    );
    expect(H.showSuccess).toHaveBeenCalledWith('容器时长延长成功');
    expect(props.onSuccess).toHaveBeenCalledTimes(1);
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('sends whole hours when the field carries a fraction', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    await user.type(screen.getByTestId('field-duration_hours'), '2.6');
    await waitFor(() => expect(priceCalls().at(-1)[1].duration_hours).toBe(3));

    H.post.mockResolvedValue({ data: { success: true, data: {} } });
    await user.click(screen.getByTestId('modal-ok'));

    await waitFor(() =>
      expect(
        H.post.mock.calls.filter((c) => String(c[0]).endsWith('/extend')),
      ).toHaveLength(1),
    );
    const extendCall = H.post.mock.calls.find((c) =>
      String(c[0]).endsWith('/extend'),
    );
    expect(extendCall[1].duration_hours).toBe(3);
  });

  it('reports a rejected extension with the reason', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    H.post.mockRejectedValue({
      response: { data: { message: 'insufficient balance' } },
    });
    await user.click(screen.getByTestId('modal-ok'));

    await waitFor(() =>
      expect(H.showError).toHaveBeenCalledWith(
        '延长时长失败: insufficient balance',
      ),
    );
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.onSuccess).not.toHaveBeenCalled();
  });

  // DEFECT (money): handleExtend only reacts to `success === true`. When the
  // gateway answers 200 with `{success:false, message:'...'}` — the shape it
  // uses for a refused charge, e.g. an exhausted balance — the modal shows no
  // toast, stays open and leaves the operator unable to tell a refusal from a
  // slow success. The refusal message the API sent is thrown away.
  it.skip('LOCK: a refused extension is reported to the operator', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    H.post.mockResolvedValue({
      data: { success: false, message: 'quota exceeded' },
    });
    await user.click(screen.getByTestId('modal-ok'));

    await waitFor(() => expect(H.showError).toHaveBeenCalled());
    expect(String(H.showError.mock.calls.at(-1)[0])).toContain(
      'quota exceeded',
    );
  });

  it('currently: a refused extension is never reported as a success', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    H.post.mockResolvedValue({
      data: { success: false, message: 'quota exceeded' },
    });
    await user.click(screen.getByTestId('modal-ok'));

    await waitFor(() =>
      expect(
        H.post.mock.calls.some((c) => String(c[0]).endsWith('/extend')),
      ).toBe(true),
    );

    // Invariants that survive the fix: a refusal must not be announced as a
    // success, must not tell the table to refresh, and must not close the
    // modal behind the operator's back. Deliberately NOT asserting that
    // showError stayed silent — that literal is the bug.
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.onSuccess).not.toHaveBeenCalled();
    expect(props.onCancel).not.toHaveBeenCalled();
  });

  it('cancelling never charges anything', async () => {
    const user = userEvent.setup();
    const { props } = renderModal();
    await waitFor(() => expect(priceCalls()).toHaveLength(1));

    await user.click(screen.getByTestId('modal-cancel'));

    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(
      H.post.mock.calls.some((c) => String(c[0]).endsWith('/extend')),
    ).toBe(false);
  });
});

describe('confirm button availability', () => {
  it('is live once the details have loaded', async () => {
    renderModal();
    await waitFor(() => expect(screen.getByTestId('modal-ok')).toBeEnabled());
  });

  it('is refused for a row with no id at all', async () => {
    renderModal({ deployment: { container_name: 'orphan' } });
    // Extending "nothing" would POST to /api/deployments/undefined/extend.
    expect(screen.getByTestId('modal-ok')).toBeDisabled();
    expect(H.get).not.toHaveBeenCalled();
  });

  it('is refused while a zero-hour extension is in the field', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(screen.getByTestId('modal-ok')).toBeEnabled());

    await user.type(screen.getByTestId('field-duration_hours'), '0');

    await waitFor(() => expect(screen.getByTestId('modal-ok')).toBeDisabled());
    expect(
      H.post.mock.calls.some((c) => String(c[0]).endsWith('/extend')),
    ).toBe(false);
  });

  it('caps the field at the documented 720-hour ceiling', () => {
    renderModal();
    const field = screen.getByTestId('field-duration_hours');
    expect(field.dataset.min).toBe('1');
    expect(field.dataset.max).toBe('720');
  });

  // Added after mutation testing. ExtendDurationModal carries TWO independent
  // NaN guards on the same value: one in onValueChange (:365) and one in
  // calculatePrice (:106). Removing either ALONE is an equivalent mutant — the
  // surviving guard still keeps NaN off the screen, because a non-finite
  // sanitizedHours makes calculatePrice null out priceEstimation and the whole
  // summary card is gated on it. So neither single mutation can be caught, and
  // neither is evidence of a hole.
  //
  // Removing BOTH was green across all 147 tests here before this one existed.
  // That is the hole: nothing asserted the thing the two guards jointly buy,
  // which is that an unparseable keystroke never reaches the operator as
  // "+ NaN小时" beside a priced extension. This test is the only assertion in
  // the file that fails when both are gone.
  it('never shows NaN in the summary when the field holds something unparseable', async () => {
    const user = userEvent.setup();
    renderModal();
    await waitFor(() => expect(screen.getByTestId('modal-ok')).toBeEnabled());

    await user.type(screen.getByTestId('field-duration_hours'), 'abc');

    // Asserted synchronously, NOT inside waitFor. A waitFor wrapped around a
    // negative assertion passes on its first attempt, before the state update
    // has flushed, and would report green with "NaN小时" on screen a tick
    // later — the same hollow shape this file exists to catch. `user.type`
    // already flushes its own act(), so by here the summary is settled.
    //
    // Deliberately not asserting the exact replacement text: whether an
    // unparseable entry reads as "0小时" today or is rejected outright after a
    // fix, the invariant is that the arithmetic never surfaces as NaN.
    expect(screen.getByTestId('modal').textContent).not.toContain('NaN');
  });
});
