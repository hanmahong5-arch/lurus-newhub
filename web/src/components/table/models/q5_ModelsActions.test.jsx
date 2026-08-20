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

/**
 * ModelsActions — the toolbar above the model catalogue.
 *
 * The interesting behaviour is the upstream sync handshake: preview first,
 * and only overwrite local model metadata after the operator has picked the
 * fields. A sync that skipped the preview would silently replace descriptions,
 * icons, tags and name rules across the whole catalogue, so both halves of
 * that branch — conflicts present and conflicts absent — are pinned here.
 *
 * Every child modal is shimmed to a marker that publishes its visibility and
 * the data it was handed, and exposes a button per callback. Nothing is
 * stubbed to `() => null`: a modal that opened but received the wrong payload
 * has to be visible in the DOM.
 */

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/react';

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

vi.mock('@douyinfe/semi-ui', () => ({
  Button: ({ children, onClick, loading, type }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': `btn-${String(children)}`,
        'data-loading': String(!!loading),
        'data-type': type,
        onClick,
      },
      children,
    ),
  Modal: ({ title, visible, onCancel, onOk, children }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'delete-modal',
        'data-visible': String(!!visible),
        'data-title': String(title),
      },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'delete-modal-ok',
        onClick: onOk,
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'delete-modal-cancel',
        onClick: onCancel,
      }),
      children,
    ),
  Popover: ({ children, content }) =>
    React.createElement(
      'span',
      { 'data-testid': 'popover' },
      children,
      React.createElement(
        'span',
        { 'data-testid': 'popover-content' },
        content,
      ),
    ),
  RadioGroup: ({ children }) => React.createElement('div', null, children),
  Radio: ({ children }) => React.createElement('label', null, children),
}));

const helpers = vi.hoisted(() => ({
  showSuccess: vi.fn(),
  showError: vi.fn(),
  copy: vi.fn(),
}));
vi.mock('../../../helpers', () => helpers);

vi.mock('../../common/ui/CompactModeToggle', () => ({
  default: ({ compactMode }) =>
    React.createElement('div', {
      'data-testid': 'compact-toggle',
      'data-compact': String(!!compactMode),
    }),
}));

// Marker + callback buttons. The selected keys are surfaced so a batch action
// firing against the wrong selection is visible.
vi.mock('./components/SelectionNotification', () => ({
  default: ({ selectedKeys, onDelete, onAddPrefill, onClear, onCopy }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'selection-notification',
        'data-selected': (selectedKeys || [])
          .map((m) => m.model_name)
          .join(','),
      },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sel-delete',
        onClick: onDelete,
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sel-prefill',
        onClick: onAddPrefill,
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sel-clear',
        onClick: onClear,
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sel-copy',
        onClick: onCopy,
      }),
    ),
}));

vi.mock('./modals/MissingModelsModal', () => ({
  default: ({ visible, onClose, onConfigureModel }) =>
    React.createElement(
      'div',
      { 'data-testid': 'missing-modal', 'data-visible': String(!!visible) },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'missing-configure',
        onClick: () => onConfigureModel('claude-3-opus'),
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'missing-close',
        onClick: onClose,
      }),
    ),
}));

vi.mock('./modals/PrefillGroupManagement', () => ({
  default: ({ visible, onClose }) =>
    React.createElement(
      'div',
      { 'data-testid': 'group-management', 'data-visible': String(!!visible) },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'group-management-close',
        onClick: onClose,
      }),
    ),
}));

vi.mock('./modals/EditPrefillGroupModal', () => ({
  default: ({ visible, editingGroup }) =>
    React.createElement('div', {
      'data-testid': 'edit-prefill',
      'data-visible': String(!!visible),
      'data-editing-group': JSON.stringify(editingGroup ?? null),
    }),
}));

vi.mock('./modals/UpstreamConflictModal', () => ({
  default: ({ visible, conflicts, onSubmit, onClose, loading }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'conflict-modal',
        'data-visible': String(!!visible),
        'data-loading': String(!!loading),
        'data-conflicts': JSON.stringify(conflicts ?? null),
      },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'conflict-submit',
        onClick: () =>
          onSubmit([{ model_name: 'gpt-4o', fields: ['description'] }]),
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'conflict-close',
        onClick: onClose,
      }),
    ),
}));

vi.mock('./modals/SyncWizardModal', () => ({
  default: ({ visible, onConfirm, onClose, loading }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'sync-wizard',
        'data-visible': String(!!visible),
        'data-loading': String(!!loading),
      },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sync-confirm-official',
        // Locale 'en' differs from the component's 'zh' default on purpose:
        // a hard-coded locale would produce a different call.
        onClick: () => onConfirm({ option: 'official', locale: 'en' }),
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sync-confirm-other',
        onClick: () => onConfirm({ option: 'local', locale: 'en' }),
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'sync-close',
        onClick: onClose,
      }),
    ),
}));

import ModelsActions from './ModelsActions';

const t = (k, opts) =>
  opts && typeof opts.count === 'number' ? `${k}|count=${opts.count}` : k;

const baseProps = (over = {}) => ({
  selectedKeys: [],
  setSelectedKeys: vi.fn(),
  setEditingModel: vi.fn(),
  setShowEdit: vi.fn(),
  batchDeleteModels: vi.fn(),
  syncing: false,
  previewing: false,
  syncUpstream: vi.fn().mockResolvedValue(undefined),
  previewUpstreamDiff: vi.fn().mockResolvedValue({ conflicts: [] }),
  applyUpstreamOverwrite: vi.fn().mockResolvedValue(true),
  compactMode: false,
  setCompactMode: vi.fn(),
  t,
  ...over,
});

const selected = [
  { id: 1, model_name: 'gpt-4o' },
  { id: 2, model_name: 'claude-3' },
];

beforeEach(() => {
  helpers.showSuccess.mockReset();
  helpers.showError.mockReset();
  helpers.copy.mockReset();
});

describe('ModelsActions — toolbar entry points', () => {
  it('opens a blank editor for 添加模型 rather than reusing a selected row', () => {
    const props = baseProps({ selectedKeys: selected });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-添加模型'));
    expect(props.setEditingModel).toHaveBeenCalledWith({ id: undefined });
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
  });

  it('keeps every modal closed until its trigger is used', () => {
    const { getByTestId } = render(<ModelsActions {...baseProps()} />);
    expect(getByTestId('missing-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
    expect(getByTestId('group-management')).toHaveAttribute(
      'data-visible',
      'false',
    );
    expect(getByTestId('sync-wizard')).toHaveAttribute('data-visible', 'false');
    expect(getByTestId('conflict-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
    expect(getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('opens and closes the unconfigured-models modal', () => {
    const { getByTestId } = render(<ModelsActions {...baseProps()} />);
    fireEvent.click(getByTestId('btn-未配置模型'));
    expect(getByTestId('missing-modal')).toHaveAttribute(
      'data-visible',
      'true',
    );
    fireEvent.click(getByTestId('missing-close'));
    expect(getByTestId('missing-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('carries the chosen unconfigured model name into a new editor and closes the picker', () => {
    const props = baseProps();
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-未配置模型'));
    fireEvent.click(getByTestId('missing-configure'));
    expect(props.setEditingModel).toHaveBeenCalledWith({
      id: undefined,
      model_name: 'claude-3-opus',
    });
    expect(props.setShowEdit).toHaveBeenCalledWith(true);
    expect(getByTestId('missing-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('opens the prefill group manager', () => {
    const { getByTestId } = render(<ModelsActions {...baseProps()} />);
    fireEvent.click(getByTestId('btn-预填组管理'));
    expect(getByTestId('group-management')).toHaveAttribute(
      'data-visible',
      'true',
    );
  });

  it('passes the compact-mode state through to the toggle', () => {
    const { getByTestId } = render(
      <ModelsActions {...baseProps({ compactMode: true })} />,
    );
    expect(getByTestId('compact-toggle')).toHaveAttribute(
      'data-compact',
      'true',
    );
  });

  // Scoped to each render's own container: three trees live in one document
  // here, and a document-wide query would match all of them.
  it('shows the sync button as busy while a sync or a preview is running', () => {
    const syncButton = (container) =>
      container.querySelector('[data-testid="btn-同步"]');

    const { container: idle } = render(<ModelsActions {...baseProps()} />);
    expect(syncButton(idle)).toHaveAttribute('data-loading', 'false');

    const { container: previewing } = render(
      <ModelsActions {...baseProps({ previewing: true })} />,
    );
    expect(syncButton(previewing)).toHaveAttribute('data-loading', 'true');

    const { container: syncing } = render(
      <ModelsActions {...baseProps({ syncing: true })} />,
    );
    expect(syncButton(syncing)).toHaveAttribute('data-loading', 'true');
  });

  it('links to the community metadata repository from the sync popover', () => {
    const { getByTestId } = render(<ModelsActions {...baseProps()} />);
    const link = getByTestId('popover-content').querySelector('a');
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/basellm/llm-metadata',
    );
    // An external target without rel=noreferrer would leak the console URL
    // through window.opener.
    expect(link).toHaveAttribute('target', '_blank');
    expect(link.getAttribute('rel')).toContain('noreferrer');
  });
});

describe('ModelsActions — upstream sync handshake', () => {
  it('previews first and syncs directly when upstream agrees with local', async () => {
    const props = baseProps();
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-同步'));
    fireEvent.click(getByTestId('sync-confirm-official'));

    await waitFor(() =>
      expect(props.previewUpstreamDiff).toHaveBeenCalledWith({ locale: 'en' }),
    );
    await waitFor(() =>
      expect(props.syncUpstream).toHaveBeenCalledWith({ locale: 'en' }),
    );
    expect(getByTestId('conflict-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  // Negative half, and the one that matters: when upstream disagrees the
  // overwrite must NOT happen behind the operator's back.
  it('stops at the conflict picker and does not sync when upstream disagrees', async () => {
    const conflicts = [
      {
        model_name: 'gpt-4o',
        fields: [{ field: 'description', local: 'ours', upstream: 'theirs' }],
      },
    ];
    const props = baseProps({
      previewUpstreamDiff: vi.fn().mockResolvedValue({ conflicts }),
    });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-同步'));
    fireEvent.click(getByTestId('sync-confirm-official'));

    await waitFor(() =>
      expect(getByTestId('conflict-modal')).toHaveAttribute(
        'data-visible',
        'true',
      ),
    );
    expect(props.syncUpstream).not.toHaveBeenCalled();
    expect(
      JSON.parse(getByTestId('conflict-modal').getAttribute('data-conflicts')),
    ).toEqual(conflicts);
  });

  it('does not sync at all when the wizard picked something other than 官方同步', async () => {
    const props = baseProps();
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-同步'));
    fireEvent.click(getByTestId('sync-confirm-other'));

    await waitFor(() =>
      expect(getByTestId('sync-wizard')).toHaveAttribute(
        'data-visible',
        'false',
      ),
    );
    expect(props.previewUpstreamDiff).not.toHaveBeenCalled();
    expect(props.syncUpstream).not.toHaveBeenCalled();
  });

  it('applies only the fields the operator ticked, under the locale they chose', async () => {
    const conflicts = [{ model_name: 'gpt-4o', fields: [{ field: 'icon' }] }];
    const props = baseProps({
      previewUpstreamDiff: vi.fn().mockResolvedValue({ conflicts }),
    });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-同步'));
    fireEvent.click(getByTestId('sync-confirm-official'));
    await waitFor(() =>
      expect(getByTestId('conflict-modal')).toHaveAttribute(
        'data-visible',
        'true',
      ),
    );

    fireEvent.click(getByTestId('conflict-submit'));
    await waitFor(() =>
      expect(props.applyUpstreamOverwrite).toHaveBeenCalledWith({
        overwrite: [{ model_name: 'gpt-4o', fields: ['description'] }],
        // 'en' came from the wizard; the component's initial state is 'zh',
        // so a stale-state read would show up here.
        locale: 'en',
      }),
    );
  });

  it('treats a preview response with no conflicts field as no conflicts', async () => {
    const props = baseProps({
      previewUpstreamDiff: vi.fn().mockResolvedValue(undefined),
    });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('btn-同步'));
    fireEvent.click(getByTestId('sync-confirm-official'));
    await waitFor(() =>
      expect(props.syncUpstream).toHaveBeenCalledWith({ locale: 'en' }),
    );
    expect(getByTestId('conflict-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });
});

describe('ModelsActions — batch actions on the selection', () => {
  it('hands the current selection to the notification bar', () => {
    const { getByTestId } = render(
      <ModelsActions {...baseProps({ selectedKeys: selected })} />,
    );
    expect(getByTestId('selection-notification')).toHaveAttribute(
      'data-selected',
      'gpt-4o,claude-3',
    );
  });

  // Deletion is irreversible: the first click must only raise the confirm.
  it('asks before deleting, and deletes only once confirmed', () => {
    const props = baseProps({ selectedKeys: selected });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('sel-delete'));
    expect(props.batchDeleteModels).not.toHaveBeenCalled();
    expect(getByTestId('delete-modal')).toHaveAttribute('data-visible', 'true');
    expect(getByTestId('delete-modal').textContent).toContain('count=2');

    fireEvent.click(getByTestId('delete-modal-ok'));
    expect(props.batchDeleteModels).toHaveBeenCalledTimes(1);
    expect(getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('deletes nothing when the confirmation is dismissed', () => {
    const props = baseProps({ selectedKeys: selected });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('sel-delete'));
    fireEvent.click(getByTestId('delete-modal-cancel'));
    expect(props.batchDeleteModels).not.toHaveBeenCalled();
    expect(getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('clears the selection outright', () => {
    const props = baseProps({ selectedKeys: selected });
    const { getByTestId } = render(<ModelsActions {...props} />);
    fireEvent.click(getByTestId('sel-clear'));
    expect(props.setSelectedKeys).toHaveBeenCalledWith([]);
  });

  it('copies every selected model name, comma separated, and confirms', async () => {
    helpers.copy.mockResolvedValue(true);
    const { getByTestId } = render(
      <ModelsActions {...baseProps({ selectedKeys: selected })} />,
    );
    fireEvent.click(getByTestId('sel-copy'));
    await waitFor(() =>
      expect(helpers.copy).toHaveBeenCalledWith('gpt-4o,claude-3'),
    );
    await waitFor(() =>
      expect(helpers.showSuccess).toHaveBeenCalledWith('已复制模型名称'),
    );
    expect(helpers.showError).not.toHaveBeenCalled();
  });

  // A refused clipboard write must not report success.
  it('reports a failed copy as a failure', async () => {
    helpers.copy.mockResolvedValue(false);
    const { getByTestId } = render(
      <ModelsActions {...baseProps({ selectedKeys: selected })} />,
    );
    fireEvent.click(getByTestId('sel-copy'));
    await waitFor(() =>
      expect(helpers.showError).toHaveBeenCalledWith('复制失败'),
    );
    expect(helpers.showSuccess).not.toHaveBeenCalled();
  });

  it('does not touch the clipboard when nothing is selected', () => {
    const { getByTestId } = render(<ModelsActions {...baseProps()} />);
    fireEvent.click(getByTestId('sel-copy'));
    expect(helpers.copy).not.toHaveBeenCalled();
    expect(helpers.showSuccess).not.toHaveBeenCalled();
  });

  it('seeds a new prefill group with the selected model names', () => {
    const { getByTestId } = render(
      <ModelsActions {...baseProps({ selectedKeys: selected })} />,
    );
    fireEvent.click(getByTestId('sel-prefill'));
    expect(getByTestId('edit-prefill')).toHaveAttribute('data-visible', 'true');
    expect(
      JSON.parse(
        getByTestId('edit-prefill').getAttribute('data-editing-group'),
      ),
    ).toEqual({ type: 'model', items: ['gpt-4o', 'claude-3'] });
  });
});
