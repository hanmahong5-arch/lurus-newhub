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

// Tooltip renders its `content` into a marker so the disabled-state copy stays
// assertable — the previous round shipped a test that stubbed a tooltip's
// content away and could no longer tell "warns" from "does not warn".
vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  Typography.Title = ({ children, ...rest }) =>
    React.createElement('h5', rest, children);
  return {
    Button: ({
      icon,
      children,
      onClick,
      disabled,
      type,
      'aria-label': label,
      className,
      style,
    }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          onClick,
          disabled: !!disabled,
          'aria-label': label,
          'data-btn-type': type,
          'data-disabled': disabled ? 'yes' : 'no',
          className,
          style,
        },
        icon,
        children,
      ),
    Tooltip: ({ content, children }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tip', 'data-tip': String(content) },
        children,
      ),
    Card: ({ children }) =>
      React.createElement('section', { 'data-testid': 'card' }, children),
    Typography,
    Chat: (props) =>
      React.createElement('div', {
        'data-testid': 'chat',
        'data-placeholder': props.placeholder,
        'data-count': String((props.chats || []).length),
      }),
  };
});

vi.mock('lucide-react', () => {
  const icon = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
  return {
    Settings: icon('settings'),
    Eye: icon('eye'),
    EyeOff: icon('eye-off'),
    RefreshCw: icon('refresh'),
    Copy: icon('copy'),
    Trash2: icon('trash'),
    UserCheck: icon('user-check'),
    Edit: icon('edit'),
    MessageSquare: icon('message'),
    ChevronRight: icon('chevron-right'),
    ChevronUp: icon('chevron-up'),
    Brain: icon('brain'),
    Loader2: icon('loader'),
  };
});

vi.mock('./CustomInputRender', () => ({
  default: () => React.createElement('div', { 'data-testid': 'input-render' }),
}));

vi.mock('../common/markdown/MarkdownRenderer', () => ({
  default: ({ content, animated, previousContentLength }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'markdown',
        'data-animated': animated ? 'yes' : 'no',
        'data-prev-len': String(previousContentLength),
      },
      content,
    ),
}));

import FloatingButtons from './FloatingButtons';
import MessageActions from './MessageActions';
import ChatArea from './ChatArea';
import ThinkingContent from './ThinkingContent';

const mobile = { isMobile: true };
const desktop = { isMobile: false };

beforeEach(() => {
  vi.clearAllMocks();
});

describe('FloatingButtons', () => {
  it('never appears on desktop, where the panels are docked', () => {
    const { container } = render(
      React.createElement(FloatingButtons, {
        styleState: desktop,
        showSettings: false,
        showDebugPanel: false,
      }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('offers settings and debug toggles on mobile and reports each click', () => {
    const onToggleSettings = vi.fn();
    const onToggleDebugPanel = vi.fn();
    render(
      React.createElement(FloatingButtons, {
        styleState: mobile,
        showSettings: false,
        showDebugPanel: false,
        onToggleSettings,
        onToggleDebugPanel,
      }),
    );
    expect(screen.getByTestId('icon-settings')).toBeInTheDocument();
    // Debug is off, so the affordance reads "show" (eye), not "hide".
    expect(screen.getByTestId('icon-eye')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-eye-off')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('icon-settings').closest('button'));
    fireEvent.click(screen.getByTestId('icon-eye').closest('button'));
    expect(onToggleSettings).toHaveBeenCalledTimes(1);
    expect(onToggleDebugPanel).toHaveBeenCalledTimes(1);
  });

  it('flips the debug toggle to a "hide" affordance in danger colours when open', () => {
    render(
      React.createElement(FloatingButtons, {
        styleState: mobile,
        showSettings: false,
        showDebugPanel: true,
      }),
    );
    expect(screen.getByTestId('icon-eye-off')).toBeInTheDocument();
    expect(
      screen.getByTestId('icon-eye-off').closest('button'),
    ).toHaveAttribute('data-btn-type', 'danger');
  });

  it('gets out of the way entirely while the settings sheet is open', () => {
    const { container } = render(
      React.createElement(FloatingButtons, {
        styleState: mobile,
        showSettings: true,
        showDebugPanel: false,
      }),
    );
    expect(container.querySelectorAll('button')).toHaveLength(0);
  });
});

describe('MessageActions', () => {
  const base = {
    styleState: desktop,
    onMessageReset: vi.fn(),
    onMessageCopy: vi.fn(),
    onMessageDelete: vi.fn(),
    onRoleToggle: vi.fn(),
    onMessageEdit: vi.fn(),
  };
  const msg = (over = {}) => ({
    id: 1,
    role: 'assistant',
    content: 'hi',
    status: 'complete',
    ...over,
  });
  const btn = (label) => screen.getByLabelText(label);

  it('offers retry, copy, edit, role-toggle and delete on a finished reply', () => {
    render(React.createElement(MessageActions, { ...base, message: msg() }));
    ['重试', '复制', '编辑', '删除'].forEach((l) =>
      expect(btn(l)).toBeInTheDocument(),
    );
    expect(btn('切换为System角色')).toBeInTheDocument();
  });

  it('withholds retry, edit, role-toggle and delete while the reply is streaming', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ status: 'loading' }),
      }),
    );
    expect(screen.queryByLabelText('重试')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('删除')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('编辑')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('切换为System角色')).not.toBeInTheDocument();
    // Copying a partial answer is still legitimate.
    expect(btn('复制')).toBeInTheDocument();
  });

  it('treats an incomplete message the same as a loading one', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ status: 'incomplete' }),
      }),
    );
    expect(screen.queryByLabelText('重试')).not.toBeInTheDocument();
  });

  it('hides copy and edit for an empty message', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ content: '' }),
      }),
    );
    expect(screen.queryByLabelText('复制')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('编辑')).not.toBeInTheDocument();
    expect(btn('重试')).toBeInTheDocument();
  });

  it('hides edit when the parent supplied no edit handler', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        onMessageEdit: undefined,
        message: msg(),
      }),
    );
    expect(screen.queryByLabelText('编辑')).not.toBeInTheDocument();
  });

  it('offers no role toggle on a user message', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ role: 'user' }),
      }),
    );
    expect(screen.queryByLabelText(/切换为/)).not.toBeInTheDocument();
  });

  it('labels the role toggle by the role it would switch to', () => {
    const { unmount } = render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ role: 'assistant' }),
      }),
    );
    expect(btn('切换为System角色')).toBeInTheDocument();
    unmount();

    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg({ role: 'system' }),
      }),
    );
    expect(btn('切换为Assistant角色')).toBeInTheDocument();
  });

  it('passes the whole message back to each handler', () => {
    const onMessageReset = vi.fn();
    const onMessageCopy = vi.fn();
    const onMessageDelete = vi.fn();
    const onRoleToggle = vi.fn();
    const onMessageEdit = vi.fn();
    const message = msg();
    render(
      React.createElement(MessageActions, {
        ...base,
        onMessageReset,
        onMessageCopy,
        onMessageDelete,
        onRoleToggle,
        onMessageEdit,
        message,
      }),
    );
    fireEvent.click(btn('重试'));
    fireEvent.click(btn('复制'));
    fireEvent.click(btn('编辑'));
    fireEvent.click(btn('切换为System角色'));
    fireEvent.click(btn('删除'));
    [
      onMessageReset,
      onMessageCopy,
      onMessageEdit,
      onRoleToggle,
      onMessageDelete,
    ].forEach((fn) => expect(fn).toHaveBeenCalledWith(message));
  });

  it('disables every mutating action while another message is generating', () => {
    const onMessageReset = vi.fn();
    const onMessageDelete = vi.fn();
    render(
      React.createElement(MessageActions, {
        ...base,
        onMessageReset,
        onMessageDelete,
        message: msg(),
        isAnyMessageGenerating: true,
      }),
    );
    expect(btn('重试')).toBeDisabled();
    expect(btn('删除')).toBeDisabled();
    expect(btn('切换为System角色')).toBeDisabled();
    // …and explains why, rather than silently going grey.
    expect(
      btn('重试').closest('[data-testid="tip"]').getAttribute('data-tip'),
    ).toBe('操作暂时被禁用');
    // Even if something dispatched a click anyway, no handler must fire.
    fireEvent.click(btn('重试'));
    fireEvent.click(btn('删除'));
    expect(onMessageReset).not.toHaveBeenCalled();
    expect(onMessageDelete).not.toHaveBeenCalled();
  });

  it('leaves copy enabled while another message is generating', () => {
    const onMessageCopy = vi.fn();
    render(
      React.createElement(MessageActions, {
        ...base,
        onMessageCopy,
        message: msg(),
        isAnyMessageGenerating: true,
      }),
    );
    expect(btn('复制')).not.toBeDisabled();
    fireEvent.click(btn('复制'));
    expect(onMessageCopy).toHaveBeenCalledTimes(1);
  });

  it('hides edit and disables the rest while this message is being edited', () => {
    render(
      React.createElement(MessageActions, {
        ...base,
        message: msg(),
        isEditing: true,
      }),
    );
    expect(screen.queryByLabelText('编辑')).not.toBeInTheDocument();
    expect(btn('重试')).toBeDisabled();
  });
});

describe('ChatArea', () => {
  const common = {
    chatRef: { current: null },
    message: [{ id: 1 }, { id: 2 }],
    inputs: { model: 'gpt-4o-mini' },
    showDebugPanel: false,
    roleInfo: {},
    onToggleDebugPanel: vi.fn(),
  };

  it('shows the selected model in the header on desktop', () => {
    render(React.createElement(ChatArea, { ...common, styleState: desktop }));
    expect(screen.getByText('gpt-4o-mini')).toBeInTheDocument();
    expect(screen.getByText('AI 对话')).toBeInTheDocument();
  });

  it('prompts the user to pick a model when none is chosen', () => {
    render(
      React.createElement(ChatArea, {
        ...common,
        inputs: {},
        styleState: desktop,
      }),
    );
    expect(screen.getByText('选择模型开始对话')).toBeInTheDocument();
  });

  it('drops the whole header on mobile, where space is scarce', () => {
    render(React.createElement(ChatArea, { ...common, styleState: mobile }));
    expect(screen.queryByText('AI 对话')).not.toBeInTheDocument();
    // The conversation itself must still be there.
    expect(screen.getByTestId('chat')).toHaveAttribute('data-count', '2');
  });

  it('toggles the debug panel and flips the button label with it', () => {
    const onToggleDebugPanel = vi.fn();
    const { unmount } = render(
      React.createElement(ChatArea, {
        ...common,
        onToggleDebugPanel,
        styleState: desktop,
      }),
    );
    fireEvent.click(screen.getByText('显示调试'));
    expect(onToggleDebugPanel).toHaveBeenCalledTimes(1);
    unmount();

    render(
      React.createElement(ChatArea, {
        ...common,
        showDebugPanel: true,
        styleState: desktop,
      }),
    );
    expect(screen.getByText('隐藏调试')).toBeInTheDocument();
  });

  it('hands the conversation and its placeholder to the chat widget', () => {
    render(React.createElement(ChatArea, { ...common, styleState: desktop }));
    const chat = screen.getByTestId('chat');
    expect(chat).toHaveAttribute('data-count', '2');
    expect(chat).toHaveAttribute('data-placeholder', '请输入您的问题...');
  });
});

describe('ThinkingContent', () => {
  const common = {
    styleState: desktop,
    onToggleReasoningExpansion: vi.fn(),
    thinkingSource: null,
  };

  it('renders nothing when there is no reasoning to show', () => {
    const { container } = render(
      React.createElement(ThinkingContent, {
        ...common,
        message: { id: 1, status: 'complete' },
        finalExtractedThinkingContent: '',
      }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('says "thinking…" with a spinner while reasoning is still streaming', () => {
    render(
      React.createElement(ThinkingContent, {
        ...common,
        message: { id: 1, status: 'loading', isThinkingComplete: false },
        finalExtractedThinkingContent: 'step one',
      }),
    );
    expect(screen.getByText('思考中...')).toBeInTheDocument();
    expect(screen.getByTestId('icon-loader')).toBeInTheDocument();
    // No expand chevron while it is still working.
    expect(screen.queryByTestId('icon-chevron-right')).not.toBeInTheDocument();
  });

  it('switches to a collapsed "thought process" header once reasoning finishes', () => {
    render(
      React.createElement(ThinkingContent, {
        ...common,
        message: { id: 1, status: 'complete', isReasoningExpanded: false },
        finalExtractedThinkingContent: 'done thinking',
      }),
    );
    expect(screen.getByText('思考过程')).toBeInTheDocument();
    expect(screen.getByTestId('icon-chevron-right')).toBeInTheDocument();
    // Collapsed: the body is not mounted at all.
    expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
  });

  it('reveals the reasoning body and an "up" chevron when expanded', () => {
    render(
      React.createElement(ThinkingContent, {
        ...common,
        message: { id: 1, status: 'complete', isReasoningExpanded: true },
        finalExtractedThinkingContent: 'because X',
      }),
    );
    expect(screen.getByTestId('icon-chevron-up')).toBeInTheDocument();
    const md = screen.getByTestId('markdown');
    expect(md).toHaveTextContent('because X');
    expect(md).toHaveAttribute('data-animated', 'no');
  });

  it('animates the reasoning body only while it is still streaming', () => {
    render(
      React.createElement(ThinkingContent, {
        ...common,
        message: {
          id: 1,
          status: 'loading',
          isThinkingComplete: false,
          isReasoningExpanded: true,
        },
        finalExtractedThinkingContent: 'partial',
      }),
    );
    expect(screen.getByTestId('markdown')).toHaveAttribute(
      'data-animated',
      'yes',
    );
  });

  it('reports how much of the streamed reasoning is already on screen', () => {
    const message = {
      id: 1,
      status: 'loading',
      isThinkingComplete: false,
      isReasoningExpanded: true,
    };
    const { rerender } = render(
      React.createElement(ThinkingContent, {
        ...common,
        message,
        finalExtractedThinkingContent: 'abc',
      }),
    );
    // First pass: nothing rendered before, so the whole string is new.
    expect(screen.getByTestId('markdown')).toHaveAttribute(
      'data-prev-len',
      '0',
    );

    rerender(
      React.createElement(ThinkingContent, {
        ...common,
        message,
        finalExtractedThinkingContent: 'abcdef',
      }),
    );
    // Second pass: 'abc' was already shown, only 'def' is new.
    expect(screen.getByTestId('markdown')).toHaveAttribute(
      'data-prev-len',
      '3',
    );
  });

  it('restarts the diff when the stream is replaced rather than extended', () => {
    const message = {
      id: 1,
      status: 'loading',
      isThinkingComplete: false,
      isReasoningExpanded: true,
    };
    const { rerender } = render(
      React.createElement(ThinkingContent, {
        ...common,
        message,
        finalExtractedThinkingContent: 'abc',
      }),
    );
    rerender(
      React.createElement(ThinkingContent, {
        ...common,
        message,
        finalExtractedThinkingContent: 'zzz totally different',
      }),
    );
    expect(screen.getByTestId('markdown')).toHaveAttribute(
      'data-prev-len',
      '0',
    );
  });

  it('discloses where the reasoning came from when a source is known', () => {
    const { unmount } = render(
      React.createElement(ThinkingContent, {
        ...common,
        thinkingSource: 'reasoning_content',
        message: { id: 1, status: 'complete' },
        finalExtractedThinkingContent: 'x',
      }),
    );
    expect(screen.getByText(/reasoning_content/)).toBeInTheDocument();
    unmount();

    render(
      React.createElement(ThinkingContent, {
        ...common,
        message: { id: 1, status: 'complete' },
        finalExtractedThinkingContent: 'x',
      }),
    );
    expect(screen.queryByText(/来源:/)).not.toBeInTheDocument();
  });

  it('toggles expansion for the message it belongs to', () => {
    const onToggleReasoningExpansion = vi.fn();
    render(
      React.createElement(ThinkingContent, {
        ...common,
        onToggleReasoningExpansion,
        message: { id: 77, status: 'complete' },
        finalExtractedThinkingContent: 'x',
      }),
    );
    fireEvent.click(screen.getByText('思考过程'));
    expect(onToggleReasoningExpansion).toHaveBeenCalledWith(77);
  });
});
