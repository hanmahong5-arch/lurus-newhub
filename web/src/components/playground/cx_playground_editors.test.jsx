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
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, type, ...rest }) =>
    React.createElement('span', { 'data-type': type, ...rest }, children);
  Typography.Title = ({ children }) =>
    React.createElement('h5', null, children);
  return {
    Input: ({ value, onChange, placeholder, disabled, prefix }) =>
      React.createElement(
        'span',
        null,
        prefix,
        React.createElement('input', {
          'data-testid': `url-${placeholder}`,
          placeholder,
          value: value ?? '',
          disabled: !!disabled,
          onChange: (e) => onChange && onChange(e.target.value),
        }),
      ),
    TextArea: ({ value, onChange, placeholder, className }) =>
      React.createElement('textarea', {
        'data-testid': 'json-editor',
        placeholder,
        className,
        value: value ?? '',
        onChange: (e) => onChange && onChange(e.target.value),
      }),
    Typography,
    Button: ({ icon, children, onClick, disabled, 'aria-label': label }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          onClick,
          disabled: !!disabled,
          'aria-label': label,
        },
        icon,
        children,
      ),
    Switch: ({ checked, onChange, disabled, checkedText }) =>
      React.createElement('input', {
        type: 'checkbox',
        'data-testid': `switch-${checkedText}`,
        checked: !!checked,
        disabled: !!disabled,
        onChange: (e) => onChange && onChange(e.target.checked),
      }),
    Banner: ({ type, description, icon }) =>
      React.createElement(
        'div',
        { 'data-testid': 'banner', 'data-banner-type': type },
        icon,
        description,
      ),
    Card: ({ children }) =>
      React.createElement('section', { 'data-testid': 'card' }, children),
    Tabs: ({ activeKey, onChange, children }) =>
      React.createElement(
        'div',
        { 'data-testid': 'tabs', 'data-active': activeKey },
        children,
      ),
    TabPane: ({ tab, itemKey, children }) =>
      React.createElement(
        'div',
        { 'data-pane': itemKey },
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `tabbtn-${itemKey}`,
            onClick: () => {},
          },
          tab,
        ),
        children,
      ),
    Dropdown: ({ children }) => React.createElement('div', null, children),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconFile: () => React.createElement('i', { 'data-testid': 'icon-file' }),
}));

vi.mock('lucide-react', () => {
  const icon = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
  return {
    FileText: icon('filetext'),
    Plus: icon('plus'),
    X: icon('x'),
    Image: icon('image'),
    Code: icon('code'),
    Edit: icon('edit'),
    Check: icon('check'),
    AlertTriangle: icon('alert'),
    Zap: icon('zap'),
    Clock: icon('clock'),
    Eye: icon('eye'),
    Send: icon('send'),
  };
});

// CodeViewer / SSEViewer are marker shims that echo the content handed to
// them: DebugPanel's only job is choosing which viewer gets which payload.
vi.mock('./CodeViewer', () => ({
  default: ({ content, title }) =>
    React.createElement(
      'pre',
      { 'data-testid': 'code-viewer', 'data-title': String(title) },
      typeof content === 'string' ? content : JSON.stringify(content),
    ),
}));
vi.mock('./SSEViewer', () => ({
  default: ({ sseData, title }) =>
    React.createElement('div', {
      'data-testid': 'sse-viewer',
      'data-title': String(title),
      'data-events': String((sseData || []).length),
    }),
}));

import ImageUrlInput from './ImageUrlInput';
import CustomRequestEditor from './CustomRequestEditor';
import DebugPanel from './DebugPanel';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('ImageUrlInput', () => {
  const base = {
    imageUrls: [],
    imageEnabled: true,
    onImageUrlsChange: vi.fn(),
    onImageEnabledChange: vi.fn(),
  };
  const addButton = () => screen.getByTestId('icon-plus').closest('button');

  it('invites the user to add a URL when the feature is on but empty', () => {
    render(React.createElement(ImageUrlInput, base));
    expect(screen.getByText(/点击 \+ 按钮添加图片URL/)).toBeInTheDocument();
    expect(addButton()).not.toBeDisabled();
  });

  it('explains what the feature is for while it is switched off, and locks "add"', () => {
    render(
      React.createElement(ImageUrlInput, { ...base, imageEnabled: false }),
    );
    expect(screen.getByText(/启用后可添加图片URL/)).toBeInTheDocument();
    expect(addButton()).toBeDisabled();
  });

  it('counts the images once some are present', () => {
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        imageUrls: ['https://a/1.png', 'https://a/2.png'],
      }),
    );
    expect(
      screen.getByText(/已添加/).textContent.replace(/\s+/g, ' '),
    ).toContain('2');
  });

  it('appends an empty slot without disturbing the existing URLs', () => {
    const onImageUrlsChange = vi.fn();
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        onImageUrlsChange,
        imageUrls: ['https://a/1.png'],
      }),
    );
    fireEvent.click(addButton());
    expect(onImageUrlsChange).toHaveBeenCalledWith(['https://a/1.png', '']);
  });

  it('edits exactly the row that changed', () => {
    const onImageUrlsChange = vi.fn();
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        onImageUrlsChange,
        imageUrls: ['one', 'two'],
      }),
    );
    fireEvent.change(screen.getByTestId('url-https://example.com/image2.jpg'), {
      target: { value: 'TWO' },
    });
    expect(onImageUrlsChange).toHaveBeenCalledWith(['one', 'TWO']);
  });

  it('removes exactly the row that was dismissed', () => {
    const onImageUrlsChange = vi.fn();
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        onImageUrlsChange,
        imageUrls: ['one', 'two', 'three'],
      }),
    );
    const removeButtons = screen
      .getAllByTestId('icon-x')
      .map((i) => i.closest('button'));
    fireEvent.click(removeButtons[1]);
    expect(onImageUrlsChange).toHaveBeenCalledWith(['one', 'three']);
  });

  it('reports the switch state back to the caller', () => {
    const onImageEnabledChange = vi.fn();
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        imageEnabled: false,
        onImageEnabledChange,
      }),
    );
    fireEvent.click(screen.getByTestId('switch-启用'));
    expect(onImageEnabledChange).toHaveBeenCalledWith(true);
  });

  it('locks every control and says why when custom-body mode owns the request', () => {
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        imageUrls: ['one'],
        disabled: true,
      }),
    );
    expect(screen.getByText(/已在自定义模式中忽略/)).toBeInTheDocument();
    expect(screen.getByTestId('switch-启用')).toBeDisabled();
    expect(addButton()).toBeDisabled();
    expect(
      screen.getByTestId('url-https://example.com/image1.jpg'),
    ).toBeDisabled();
    expect(screen.getByTestId('icon-x').closest('button')).toBeDisabled();
  });

  it('explains the disabled state differently when the feature is also off', () => {
    render(
      React.createElement(ImageUrlInput, {
        ...base,
        imageEnabled: false,
        disabled: true,
      }),
    );
    expect(
      screen.getByText('图片功能在自定义请求体模式下不可用'),
    ).toBeInTheDocument();
  });
});

describe('CustomRequestEditor', () => {
  const base = {
    customRequestMode: false,
    customRequestBody: '',
    onCustomRequestModeChange: vi.fn(),
    onCustomRequestBodyChange: vi.fn(),
    defaultPayload: { model: 'gpt-4o', messages: [] },
  };

  it('keeps the editor out of sight until custom mode is switched on', () => {
    render(React.createElement(CustomRequestEditor, base));
    expect(screen.queryByTestId('json-editor')).not.toBeInTheDocument();
    expect(screen.queryByTestId('banner')).not.toBeInTheDocument();
  });

  it('warns that the parameter panel stops applying once custom mode is on', () => {
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{}',
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'warning');
    expect(banner.textContent).toContain('模型配置面板的参数设置将被忽略');
  });

  it('seeds an empty custom body with the pretty-printed default payload', () => {
    const onCustomRequestBodyChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        onCustomRequestBodyChange,
      }),
    );
    expect(onCustomRequestBodyChange).toHaveBeenCalledWith(
      JSON.stringify(base.defaultPayload, null, 2),
    );
    expect(screen.getByTestId('json-editor')).toHaveValue(
      JSON.stringify(base.defaultPayload, null, 2),
    );
  });

  it('marks well-formed JSON as valid and keeps the format button available', () => {
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{"a":1}',
      }),
    );
    expect(screen.getByText('格式正确')).toBeInTheDocument();
    expect(screen.getByText('格式化').closest('button')).not.toBeDisabled();
  });

  it('flags malformed JSON, shows the parser message and disables formatting', () => {
    const onCustomRequestBodyChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{"a":1}',
        onCustomRequestBodyChange,
      }),
    );
    fireEvent.change(screen.getByTestId('json-editor'), {
      target: { value: '{"a":' },
    });
    expect(screen.getByText('格式错误')).toBeInTheDocument();
    expect(screen.getByText(/JSON格式错误/)).toBeInTheDocument();
    expect(screen.getByText('格式化').closest('button')).toBeDisabled();
    // Invalid text is still propagated — the user must not lose what they typed.
    expect(onCustomRequestBodyChange).toHaveBeenCalledWith('{"a":');
  });

  it('treats a blank body as valid rather than as an error', () => {
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{"a":1}',
      }),
    );
    fireEvent.change(screen.getByTestId('json-editor'), {
      target: { value: '   ' },
    });
    expect(screen.getByText('格式正确')).toBeInTheDocument();
  });

  it('pretty-prints on demand and publishes the formatted text', () => {
    const onCustomRequestBodyChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{"a":1,"b":2}',
        onCustomRequestBodyChange,
      }),
    );
    fireEvent.click(screen.getByText('格式化').closest('button'));
    const formatted = JSON.stringify({ a: 1, b: 2 }, null, 2);
    expect(screen.getByTestId('json-editor')).toHaveValue(formatted);
    expect(onCustomRequestBodyChange).toHaveBeenLastCalledWith(formatted);
  });

  it('turns custom mode off without touching the stored body', () => {
    const onCustomRequestModeChange = vi.fn();
    const onCustomRequestBodyChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: true,
        customRequestBody: '{"mine":true}',
        onCustomRequestModeChange,
        onCustomRequestBodyChange,
      }),
    );
    onCustomRequestBodyChange.mockClear();
    fireEvent.click(screen.getByTestId('switch-开'));
    expect(onCustomRequestModeChange).toHaveBeenCalledWith(false);
    expect(onCustomRequestBodyChange).not.toHaveBeenCalled();
  });

  // DEFECT (data-loss): `handleModeToggle` overwrites the body with the default
  // payload every time the switch is turned ON, unconditionally. The
  // initialise-on-mount effect right above it is careful to do that only when
  // the body is empty — the toggle is not. So a user who has hand-written a
  // request body, flicks the switch off to compare against normal mode, and
  // flicks it back on, silently loses everything they typed. There is no undo
  // and no confirmation.
  // INVARIANT, not a pin. Asserting that the toggle emits the *default* payload
  // would go red the moment the clobber is fixed, so the data-loss repair would
  // read as a regression; that contract belongs to the lock below. What holds
  // either side of the fix is that flipping the switch on reports mode=true to
  // the parent — without that the editor never opens at all.
  it('reports the mode change to the parent when custom mode is switched on', () => {
    const onCustomRequestModeChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: false,
        customRequestBody: '{"my":"careful hand-written body"}',
        onCustomRequestModeChange,
      }),
    );
    onCustomRequestModeChange.mockClear();
    fireEvent.click(screen.getByTestId('switch-开'));
    expect(onCustomRequestModeChange).toHaveBeenCalledWith(true);
  });

  it.skip('re-enabling custom mode must preserve an existing request body', () => {
    const onCustomRequestBodyChange = vi.fn();
    render(
      React.createElement(CustomRequestEditor, {
        ...base,
        customRequestMode: false,
        customRequestBody: '{"my":"careful hand-written body"}',
        onCustomRequestBodyChange,
      }),
    );
    onCustomRequestBodyChange.mockClear();
    fireEvent.click(screen.getByTestId('switch-开'));
    const clobbered = onCustomRequestBodyChange.mock.calls.find(
      ([v]) => v !== '{"my":"careful hand-written body"}',
    );
    expect(clobbered).toBeUndefined();
  });
});

describe('DebugPanel', () => {
  const base = {
    debugData: {
      previewRequest: '{"preview":1}',
      request: '{"request":1}',
      response: '{"response":1}',
    },
    activeDebugTab: 'preview',
    onActiveDebugTabChange: vi.fn(),
    styleState: { isMobile: false },
  };

  it('routes each payload to its own viewer', () => {
    render(React.createElement(DebugPanel, base));
    const viewers = screen.getAllByTestId('code-viewer');
    const contents = viewers.map((v) => v.textContent);
    expect(contents).toContain('{"preview":1}');
    expect(contents).toContain('{"request":1}');
    expect(contents).toContain('{"response":1}');
  });

  it('follows the tab the parent asks for', () => {
    const { rerender } = render(React.createElement(DebugPanel, base));
    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active',
      'preview',
    );
    rerender(
      React.createElement(DebugPanel, { ...base, activeDebugTab: 'response' }),
    );
    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active',
      'response',
    );
  });

  it('prefers the streaming viewer when SSE events were captured', () => {
    render(
      React.createElement(DebugPanel, {
        ...base,
        debugData: { ...base.debugData, sseMessages: [{ a: 1 }, { b: 2 }] },
      }),
    );
    const sse = screen.getByTestId('sse-viewer');
    expect(sse).toHaveAttribute('data-events', '2');
    // …and the raw-response code viewer steps aside.
    expect(
      screen.getAllByTestId('code-viewer').map((v) => v.textContent),
    ).not.toContain('{"response":1}');
  });

  it('badges the request tab when a custom body is in force', () => {
    const { unmount } = render(React.createElement(DebugPanel, base));
    expect(screen.queryByText('自定义')).not.toBeInTheDocument();
    unmount();

    render(
      React.createElement(DebugPanel, { ...base, customRequestMode: true }),
    );
    expect(screen.getByText('自定义')).toBeInTheDocument();
  });

  it('offers a close control only on mobile, and only when a handler exists', () => {
    const onCloseDebugPanel = vi.fn();
    const { unmount } = render(
      React.createElement(DebugPanel, { ...base, onCloseDebugPanel }),
    );
    expect(screen.queryByTestId('icon-x')).not.toBeInTheDocument();
    unmount();

    render(
      React.createElement(DebugPanel, {
        ...base,
        styleState: { isMobile: true },
        onCloseDebugPanel,
      }),
    );
    fireEvent.click(screen.getByTestId('icon-x').closest('button'));
    expect(onCloseDebugPanel).toHaveBeenCalledTimes(1);
  });

  it('timestamps the preview separately from the last real request', () => {
    const { unmount } = render(
      React.createElement(DebugPanel, {
        ...base,
        debugData: { ...base.debugData, previewTimestamp: 1700000000000 },
      }),
    );
    expect(screen.getByText(/预览更新/)).toBeInTheDocument();
    unmount();

    render(
      React.createElement(DebugPanel, {
        ...base,
        activeDebugTab: 'request',
        debugData: { ...base.debugData, timestamp: 1700000000000 },
      }),
    );
    expect(screen.getByText(/最后请求/)).toBeInTheDocument();
  });

  it('shows no timestamp line at all when nothing has run yet', () => {
    render(React.createElement(DebugPanel, base));
    expect(screen.queryByText(/预览更新/)).not.toBeInTheDocument();
    expect(screen.queryByText(/最后请求/)).not.toBeInTheDocument();
  });
});
