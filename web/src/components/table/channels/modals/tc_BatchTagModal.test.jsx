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

// Batch tag assignment. The one thing that really matters is that the count
// shown to the operator is the count of channels the write will actually
// touch — this dialog is the last screen before a bulk mutation, and the
// number on it is the only blast-radius signal there is.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: ({ visible, title, children, onOk, onCancel, maskClosable }) =>
    visible
      ? React.createElement(
          'div',
          {
            'data-testid': 'modal',
            role: 'dialog',
            'data-maskclosable': String(maskClosable),
          },
          React.createElement('span', { 'data-testid': 'modal-title' }, title),
          children,
          React.createElement(
            'button',
            { type: 'button', 'data-testid': 'modal-ok', onClick: onOk },
            'ok',
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-cancel',
              onClick: onCancel,
            },
            'cancel',
          ),
        )
      : null,
  Input: ({ value, onChange, placeholder }) =>
    React.createElement('input', {
      'data-testid': 'tag-input',
      value: value ?? '',
      placeholder,
      onChange: (e) => onChange(e.target.value),
    }),
  Typography: {
    Text: ({ children, type }) =>
      React.createElement('span', { 'data-type': type }, children),
  },
}));

import BatchTagModal from './BatchTagModal';

const t = (k) => k;

const makeProps = (over = {}) => ({
  t,
  showBatchSetTag: true,
  setShowBatchSetTag: vi.fn(),
  batchSetChannelTag: vi.fn(),
  batchSetTagValue: '',
  setBatchSetTagValue: vi.fn(),
  selectedChannels: [{ id: 1 }, { id: 2 }],
  ...over,
});

const renderModal = (over = {}) => {
  const props = makeProps(over);
  render(<BatchTagModal {...props} />);
  return props;
};

beforeEach(() => vi.clearAllMocks());

describe('BatchTagModal', () => {
  it('renders nothing while closed', () => {
    renderModal({ showBatchSetTag: false });
    expect(screen.queryByTestId('modal')).toBeNull();
  });

  it('states how many channels the write will touch', () => {
    renderModal({ selectedChannels: [{ id: 1 }, { id: 2 }, { id: 3 }] });
    expect(screen.getByText('已选择 3 个渠道')).toBeInTheDocument();
    // The literal placeholder must be substituted, not printed.
    expect(screen.queryByText(/\$\{count\}/)).toBeNull();
  });

  it('says zero rather than a stale number when nothing is selected', () => {
    renderModal({ selectedChannels: [] });
    expect(screen.getByText('已选择 0 个渠道')).toBeInTheDocument();
  });

  it('reflects the current tag value into the field', () => {
    renderModal({ batchSetTagValue: 'eu-west' });
    expect(screen.getByTestId('tag-input')).toHaveValue('eu-west');
  });

  it('reports typing straight up as the bare string Semi hands it', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.type(screen.getByTestId('tag-input'), 'x');
    // Semi's Input gives the value, not an event; passing an event object
    // upward would store "[object Object]" as the tag name.
    expect(props.setBatchSetTagValue).toHaveBeenCalledWith('x');
  });

  it('applies the tag on confirm', async () => {
    const user = userEvent.setup();
    const props = renderModal({ batchSetTagValue: 'eu-west' });
    await user.click(screen.getByTestId('modal-ok'));
    expect(props.batchSetChannelTag).toHaveBeenCalledTimes(1);
  });

  it('closes on cancel without writing anything', async () => {
    const user = userEvent.setup();
    const props = renderModal({ batchSetTagValue: 'eu-west' });
    await user.click(screen.getByTestId('modal-cancel'));
    expect(props.setShowBatchSetTag).toHaveBeenCalledWith(false);
    expect(props.batchSetChannelTag).not.toHaveBeenCalled();
  });

  it('cannot be dismissed by a stray mask click mid-edit', () => {
    renderModal();
    expect(screen.getByTestId('modal').dataset.maskclosable).toBe('false');
  });
});
