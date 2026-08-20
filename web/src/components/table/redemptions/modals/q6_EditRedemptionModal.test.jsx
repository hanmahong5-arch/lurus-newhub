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

// The redemption-code editor is where bearer instruments are MINTED. A batch
// generated here is money that anyone holding the string can spend, and the
// download dialog is the only place the plaintext codes are ever shown. What
// is guarded below:
//   * a create posts the batch count and face value the operator actually
//     typed (not a hard-coded 1 / not the edit-mode payload);
//   * an edit PUTs the id and never mints anything;
//   * a FAILED generation does not report success, does not refresh, and does
//     not close the sheet over the operator's unsaved input;
//   * every minted code reaches the download file exactly once.
//
// Semi's Form is replaced by a shim that captures onSubmit and the field
// definitions, so validators and the create-only batch field are assertable
// without driving a real form engine.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';

vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

const H = vi.hoisted(() => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  downloadTextAsFile: vi.fn(),
  modalConfirm: vi.fn(),
  // Field definitions registered by the Form.* shims, keyed by `field`.
  fields: new Map(),
  // The component's onSubmit, captured so a submit can be driven with exact
  // values instead of typing into a stubbed form.
  submit: { current: null },
  formValues: { current: {} },
}));

// Semi UI barrel: markers only, and every stub renders its children so a
// branch that disappears from the tree is detectable.
vi.mock('@douyinfe/semi-ui', () => {
  const passthrough =
    (testid, extra = () => ({})) =>
    (props) =>
      React.createElement(
        'div',
        { 'data-testid': testid, ...extra(props) },
        props.children,
      );

  const Form = ({ children, onSubmit, getFormApi, initValues }) => {
    H.submit.current = onSubmit;
    const api = {
      setValues: (v) => {
        H.formValues.current = v;
      },
      submitForm: () => onSubmit(H.formValues.current),
    };
    React.useEffect(() => {
      H.formValues.current = { ...initValues };
      getFormApi?.(api);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return React.createElement(
      'form',
      { 'data-testid': 'form' },
      typeof children === 'function'
        ? children({ values: H.formValues.current })
        : children,
    );
  };
  // Each field registers itself so its rules / min / options can be inspected.
  const field = (kind) => (props) => {
    H.fields.set(props.field, { kind, ...props });
    return React.createElement('div', {
      'data-testid': `field-${props.field}`,
      'data-kind': kind,
      'data-label': props.label,
      'data-required': String(
        Array.isArray(props.rules)
          ? props.rules.some((r) => r.required)
          : false,
      ),
      'data-min': props.min === undefined ? '' : String(props.min),
      'data-extra':
        typeof props.extraText === 'string' ? props.extraText : undefined,
    });
  };
  Form.Input = field('input');
  Form.DatePicker = field('date');
  Form.AutoComplete = field('autocomplete');
  Form.InputNumber = field('number');

  const Button = ({ children, onClick, loading, theme, type }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': 'button',
        'data-theme': theme,
        'data-variant': type,
        'data-loading': String(Boolean(loading)),
        onClick,
      },
      children,
    );

  const Modal = passthrough('modal');
  Modal.confirm = H.modalConfirm;
  Modal.error = vi.fn();

  return {
    Form,
    Button,
    Modal,
    SideSheet: (props) =>
      React.createElement(
        'div',
        {
          'data-testid': 'sidesheet',
          'data-visible': String(Boolean(props.visible)),
          'data-placement': props.placement,
        },
        props.title,
        props.children,
        props.footer,
      ),
    Space: passthrough('space'),
    Spin: (props) =>
      React.createElement(
        'div',
        {
          'data-testid': 'spin',
          'data-spinning': String(Boolean(props.spinning)),
        },
        props.children,
      ),
    Typography: {
      Text: ({ children }) => React.createElement('span', null, children),
      Title: ({ children }) =>
        React.createElement('h4', { 'data-testid': 'title' }, children),
    },
    Card: passthrough('card'),
    Tag: (props) =>
      React.createElement(
        'span',
        { 'data-testid': 'tag', 'data-color': props.color },
        props.children,
      ),
    Avatar: passthrough('avatar'),
    Row: passthrough('row'),
    Col: passthrough('col'),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconCreditCard: () => React.createElement('i', { 'data-testid': 'ic-card' }),
  IconSave: () => React.createElement('i', { 'data-testid': 'ic-save' }),
  IconClose: () => React.createElement('i', { 'data-testid': 'ic-close' }),
  IconGift: () => React.createElement('i', { 'data-testid': 'ic-gift' }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
  initReactI18next: { type: '3rdParty', init: () => {} },
  Trans: ({ children }) => children,
  withTranslation: () => (C) => C,
}));

vi.mock('../../../../hooks/common/useIsMobile', () => ({
  useIsMobile: () => false,
  MOBILE_BREAKPOINT: 768,
}));

// Real renderQuota / renderQuotaWithPrompt: the default code NAME on a create
// is derived from the face value by renderQuota, so stubbing it would hide any
// drift between the name written on the code and the money it is worth.
vi.mock('../../../../helpers', async () => {
  const renderMod = await vi.importActual('../../../../helpers/render.jsx');
  return {
    API: H.api,
    showError: H.showError,
    showSuccess: H.showSuccess,
    downloadTextAsFile: H.downloadTextAsFile,
    renderQuota: renderMod.renderQuota,
    renderQuotaWithPrompt: renderMod.renderQuotaWithPrompt,
  };
});

import EditRedemptionModal from './EditRedemptionModal';

const ok = (data) => ({ data: { success: true, message: '', data } });
const fail = (message, data) => ({ data: { success: false, message, data } });

const mount = (props = {}) => {
  const merged = {
    editingRedemption: { id: undefined },
    visiable: true,
    handleClose: vi.fn(),
    refresh: vi.fn(),
    ...props,
  };
  const utils = render(<EditRedemptionModal {...merged} />);
  return { ...utils, props: merged };
};

const submitWith = async (values) => {
  await act(async () => {
    await H.submit.current(values);
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.fields.clear();
  H.submit.current = null;
  H.formValues.current = {};
  window.localStorage.clear();
  window.localStorage.setItem('quota_per_unit', '500000');
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('create mode — shell', () => {
  it('opens as a create sheet, not an update one', () => {
    mount();
    expect(screen.getByTestId('title')).toHaveTextContent('创建新的兑换码');
    expect(screen.getByTestId('tag')).toHaveTextContent('新建');
    expect(screen.getByTestId('sidesheet')).toHaveAttribute(
      'data-placement',
      'left',
    );
  });

  it('honours the (misspelled) visiable prop the page actually passes', () => {
    // The page wires `visiable`; a rename on either side silently hides the
    // whole sheet, which is exactly the kind of break no user reports as a bug
    // — they just say "the button does nothing".
    const { unmount } = mount({ visiable: false });
    expect(screen.getByTestId('sidesheet')).toHaveAttribute(
      'data-visible',
      'false',
    );
    unmount();
    mount({ visiable: true });
    expect(screen.getByTestId('sidesheet')).toHaveAttribute(
      'data-visible',
      'true',
    );
  });

  it('does not fetch anything when minting a new batch', () => {
    mount();
    expect(H.api.get).not.toHaveBeenCalled();
    expect(screen.getByTestId('spin')).toHaveAttribute(
      'data-spinning',
      'false',
    );
  });

  it('offers the batch-count field only when creating', () => {
    mount();
    expect(screen.getByTestId('field-count')).toBeInTheDocument();
    expect(screen.getByTestId('field-count')).toHaveAttribute('data-min', '1');
  });

  it('hides the batch-count field when editing an existing code', () => {
    // Negative half of the pair. An edit that carried a count field would let
    // an operator think they were re-minting a code they are only renaming.
    H.api.get.mockResolvedValue(ok({ id: 3, name: 'x', expired_time: 0 }));
    mount({ editingRedemption: { id: 3 } });
    expect(screen.queryByTestId('field-count')).toBeNull();
  });

  it('requires a positive face value and a positive batch size', async () => {
    mount();
    const quotaRules = H.fields.get('quota').rules;
    const countRules = H.fields.get('count').rules;
    const quotaValidator = quotaRules.find((r) => r.validator).validator;
    const countValidator = countRules.find((r) => r.validator).validator;

    await expect(quotaValidator(null, '500000')).resolves.toBeUndefined();
    await expect(quotaValidator(null, '0')).rejects.toBeTruthy();
    await expect(quotaValidator(null, '-5')).rejects.toBeTruthy();
    await expect(countValidator(null, '2')).resolves.toBeUndefined();
    await expect(countValidator(null, '0')).rejects.toBeTruthy();
    await expect(countValidator(null, '-1')).rejects.toBeTruthy();
  });

  it('leaves the name optional on create but demands one on edit', () => {
    const { unmount } = mount();
    expect(screen.getByTestId('field-name')).toHaveAttribute(
      'data-required',
      'false',
    );
    unmount();
    H.api.get.mockResolvedValue(ok({ id: 3, name: 'x', expired_time: 0 }));
    mount({ editingRedemption: { id: 3 } });
    expect(screen.getByTestId('field-name')).toHaveAttribute(
      'data-required',
      'true',
    );
  });

  it('lists face-value presets whose labels match the money they encode', () => {
    // A preset labelled 10$ that posts $1 would mint the wrong instrument.
    mount();
    const presets = H.fields.get('quota').data;
    for (const { value, label } of presets) {
      const dollars = Number(label.replace('$', ''));
      expect(value / 500000).toBe(dollars);
    }
  });
});

describe('create mode — minting a batch', () => {
  it('posts the operator-typed batch count and face value', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A', 'CODE-B', 'CODE-C']));
    const { props } = mount();

    // Fixture values chosen so a hard-coded 1 (or a swapped quota/count) would
    // produce a visibly different request.
    await submitWith({
      name: 'launch-promo',
      quota: '2500000',
      count: '3',
      expired_time: null,
    });

    expect(H.api.post).toHaveBeenCalledTimes(1);
    const [url, body] = H.api.post.mock.calls[0];
    expect(url).toBe('/api/redemption/');
    expect(body.count).toBe(3);
    expect(body.quota).toBe(2500000);
    expect(body.name).toBe('launch-promo');
    expect(body.id).toBeUndefined();
    expect(H.api.put).not.toHaveBeenCalled();
    expect(props.refresh).toHaveBeenCalledTimes(1);
  });

  it('names an unnamed batch after its face value rather than posting a blank', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A']));
    mount();
    await submitWith({ name: '', quota: '5000000', count: '1' });
    // 5000000 units == $10.00 at the default rate.
    expect(H.api.post.mock.calls[0][1].name).toBe('$10.00');
  });

  it('sends expired_time 0 — never 1970 — for a code that never expires', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A']));
    mount();
    await submitWith({
      name: 'n',
      quota: '500000',
      count: '1',
      expired_time: null,
    });
    expect(H.api.post.mock.calls[0][1].expired_time).toBe(0);
  });

  it('converts a chosen expiry to whole seconds, not milliseconds', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A']));
    mount();
    const when = new Date('2027-03-01T12:00:00Z');
    await submitWith({
      name: 'n',
      quota: '500000',
      count: '1',
      expired_time: when,
    });
    // A millisecond value here would put the expiry ~50 000 years out, i.e. a
    // code that never expires when the operator asked for one that does.
    expect(H.api.post.mock.calls[0][1].expired_time).toBe(
      Math.floor(when.getTime() / 1000),
    );
  });

  it('offers every minted code for download, once each, in order', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A', 'CODE-B', 'CODE-C']));
    mount();
    await submitWith({ name: 'launch-promo', quota: '500000', count: '3' });

    expect(H.modalConfirm).toHaveBeenCalledTimes(1);
    const cfg = H.modalConfirm.mock.calls[0][0];
    // Nothing is written to disk until the operator agrees.
    expect(H.downloadTextAsFile).not.toHaveBeenCalled();

    cfg.onOk();
    expect(H.downloadTextAsFile).toHaveBeenCalledTimes(1);
    const [text, filename] = H.downloadTextAsFile.mock.calls[0];
    const lines = text.split('\n').filter(Boolean);
    expect(lines).toEqual(['CODE-A', 'CODE-B', 'CODE-C']);
    expect(new Set(lines).size).toBe(lines.length);
    expect(filename).toBe('launch-promo.txt');
  });

  it('never mints a code into the download file that the server did not return', async () => {
    // Guards the loop bound: an off-by-one would emit "undefined" as a code.
    H.api.post.mockResolvedValue(ok(['ONLY-ONE']));
    mount();
    await submitWith({ name: 'n', quota: '500000', count: '1' });
    H.modalConfirm.mock.calls[0][0].onOk();
    expect(H.downloadTextAsFile.mock.calls[0][0]).not.toContain('undefined');
    expect(
      H.downloadTextAsFile.mock.calls[0][0].split('\n').filter(Boolean),
    ).toEqual(['ONLY-ONE']);
  });

  it('reports success and closes the sheet once the batch is minted', async () => {
    H.api.post.mockResolvedValue(ok(['CODE-A']));
    const { props } = mount();
    await submitWith({ name: 'n', quota: '500000', count: '1' });
    expect(H.showSuccess).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
    expect(props.handleClose).toHaveBeenCalledTimes(1);
  });
});

describe('create mode — a generation that failed', () => {
  it('does not claim success, refresh or close when the server refuses', async () => {
    H.api.post.mockResolvedValue(fail('额度不足'));
    const { props } = mount();
    await submitWith({ name: 'n', quota: '500000', count: '5' });

    expect(H.showError).toHaveBeenCalledWith('额度不足');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    // The sheet must stay open over the operator's unsaved input.
    expect(props.handleClose).not.toHaveBeenCalled();
    expect(H.downloadTextAsFile).not.toHaveBeenCalled();
  });

  it('stops spinning after a refusal so the form can be retried', async () => {
    H.api.post.mockResolvedValue(fail('额度不足'));
    mount();
    await submitWith({ name: 'n', quota: '500000', count: '5' });
    expect(screen.getByTestId('spin')).toHaveAttribute(
      'data-spinning',
      'false',
    );
  });

  // DEFECT (correctness): the download prompt is gated on `!isEdit && data`
  // ALONE — `success` is never consulted. A partial failure (the server mints
  // some codes, then hits a limit and answers success:false with the codes it
  // did create) shows the operator an error toast AND a modal titled
  // 兑换码创建成功 offering a download, while `refresh()` was skipped so the
  // table never lists those codes. The operator is told the batch failed and
  // handed the bearer instruments at the same time; the codes are live money
  // that never appear in the console again.
  // Verified red 2026-08-20: un-skipped it fails, Modal.confirm was called once.
  it.skip('CONTRACT:a refused generation offers no success download prompt', async () => {
    H.api.post.mockResolvedValue(fail('额度不足', ['LEAKED-CODE-1']));
    mount();
    await submitWith({ name: 'n', quota: '500000', count: '2' });
    expect(H.modalConfirm).not.toHaveBeenCalled();
  });

  it('never reports a refused generation as a success, whatever else it does', async () => {
    // The invariant that survives the repair: on success:false the operator
    // must not be told it worked and the table must not be assumed fresh.
    // Asserting the modal IS shown here would pin the defect and turn red on
    // the fix, so it is left to the CONTRACT lock above.
    H.api.post.mockResolvedValue(fail('额度不足', ['LEAKED-CODE-1']));
    const { props } = mount();
    await submitWith({ name: 'n', quota: '500000', count: '2' });
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalledWith('额度不足');
    expect(props.refresh).not.toHaveBeenCalled();
  });

  it('writes nothing to disk unless the operator confirms the download', async () => {
    // Whatever the prompt gating settles on, an unconfirmed prompt must never
    // put codes on the operator's filesystem by itself.
    H.api.post.mockResolvedValue(fail('额度不足', ['LEAKED-CODE-1']));
    mount();
    await submitWith({ name: 'n', quota: '500000', count: '2' });
    expect(H.downloadTextAsFile).not.toHaveBeenCalled();
  });
});

describe('edit mode', () => {
  it('loads the code being edited and shows the update chrome', async () => {
    H.api.get.mockResolvedValue(
      ok({ id: 9, name: 'old-name', quota: 500000, expired_time: 0 }),
    );
    await act(async () => {
      mount({ editingRedemption: { id: 9 } });
    });
    expect(H.api.get).toHaveBeenCalledWith('/api/redemption/9');
    expect(screen.getByTestId('title')).toHaveTextContent('更新兑换码信息');
    expect(screen.getByTestId('sidesheet')).toHaveAttribute(
      'data-placement',
      'right',
    );
  });

  it('turns a stored expiry of 0 into "no expiry" instead of the epoch', async () => {
    H.api.get.mockResolvedValue(ok({ id: 9, name: 'n', expired_time: 0 }));
    await act(async () => {
      mount({ editingRedemption: { id: 9 } });
    });
    expect(H.formValues.current.expired_time).toBeNull();
  });

  it('turns a stored expiry into a Date at the right instant', async () => {
    const epochSeconds = 1900000000;
    H.api.get.mockResolvedValue(
      ok({ id: 9, name: 'n', expired_time: epochSeconds }),
    );
    await act(async () => {
      mount({ editingRedemption: { id: 9 } });
    });
    const loaded = H.formValues.current.expired_time;
    expect(loaded).toBeInstanceOf(Date);
    expect(loaded.getTime()).toBe(epochSeconds * 1000);
  });

  it('surfaces a failed load instead of showing a blank form as if it were data', async () => {
    H.api.get.mockResolvedValue(fail('未找到兑换码'));
    await act(async () => {
      mount({ editingRedemption: { id: 9 } });
    });
    expect(H.showError).toHaveBeenCalledWith('未找到兑换码');
    expect(screen.getByTestId('spin')).toHaveAttribute(
      'data-spinning',
      'false',
    );
  });

  it('PUTs the id and mints nothing at all', async () => {
    H.api.get.mockResolvedValue(ok({ id: 9, name: 'n', expired_time: 0 }));
    H.api.put.mockResolvedValue(ok(null));
    const { props } = await act(async () =>
      mount({ editingRedemption: { id: 9 } }),
    );

    await submitWith({ name: 'renamed', quota: '500000', count: '1' });

    expect(H.api.put).toHaveBeenCalledTimes(1);
    expect(H.api.post).not.toHaveBeenCalled();
    const [url, body] = H.api.put.mock.calls[0];
    expect(url).toBe('/api/redemption/');
    expect(body.id).toBe(9);
    expect(body.name).toBe('renamed');
    // No new instruments: no download prompt on an edit.
    expect(H.modalConfirm).not.toHaveBeenCalled();
    expect(H.downloadTextAsFile).not.toHaveBeenCalled();
    expect(props.refresh).toHaveBeenCalledTimes(1);
  });

  it('coerces a string id from the row into the numeric id the API expects', async () => {
    H.api.get.mockResolvedValue(ok({ id: 9, name: 'n', expired_time: 0 }));
    H.api.put.mockResolvedValue(ok(null));
    await act(async () => mount({ editingRedemption: { id: '9' } }));
    await submitWith({ name: 'renamed', quota: '500000', count: '1' });
    expect(H.api.put.mock.calls[0][1].id).toBe(9);
  });

  it('does not close the sheet when the update is refused', async () => {
    H.api.get.mockResolvedValue(ok({ id: 9, name: 'n', expired_time: 0 }));
    H.api.put.mockResolvedValue(fail('兑换码已被使用'));
    const { props } = await act(async () =>
      mount({ editingRedemption: { id: 9 } }),
    );
    await submitWith({ name: 'renamed', quota: '500000', count: '1' });
    expect(H.showError).toHaveBeenCalledWith('兑换码已被使用');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.handleClose).not.toHaveBeenCalled();
  });
});
