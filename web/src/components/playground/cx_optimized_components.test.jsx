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
import { render } from '@testing-library/react';

// Each wrapped component becomes a render counter. That is the only observable
// these memo comparators have: "did the wrapper let the re-render through?".
const renders = vi.hoisted(() => ({
  content: 0,
  actions: 0,
  settings: 0,
  debug: 0,
}));
// …and it also keeps the props each wrapped component was actually handed.
// A stub that only counts renders cannot tell "re-rendered with the new
// message" from "re-rendered with the old one".
const lastProps = vi.hoisted(() => ({}));
const counter = vi.hoisted(() => (key) => (props) => {
  renders[key] += 1;
  lastProps[key] = props;
  return null;
});

vi.mock('./MessageContent', () => ({ default: counter('content') }));
vi.mock('./MessageActions', () => ({ default: counter('actions') }));
vi.mock('./SettingsPanel', () => ({ default: counter('settings') }));
vi.mock('./DebugPanel', () => ({ default: counter('debug') }));

import {
  OptimizedMessageContent,
  OptimizedMessageActions,
  OptimizedSettingsPanel,
  OptimizedDebugPanel,
} from './OptimizedComponents';

beforeEach(() => {
  renders.content = 0;
  renders.actions = 0;
  renders.settings = 0;
  renders.debug = 0;
});

const message = (over = {}) => ({
  id: 1,
  content: 'hello',
  status: 'complete',
  role: 'assistant',
  reasoningContent: '',
  isReasoningExpanded: false,
  ...over,
});

describe('OptimizedMessageContent', () => {
  const props = (over = {}) => ({
    message: message(),
    isEditing: false,
    editValue: '',
    styleState: { isMobile: false },
    ...over,
  });

  it('renders once and then stays put when nothing meaningful changed', () => {
    const { rerender } = render(
      React.createElement(OptimizedMessageContent, props()),
    );
    expect(renders.content).toBe(1);
    // A brand-new but equivalent props object must not cause a re-render.
    rerender(React.createElement(OptimizedMessageContent, props()));
    expect(renders.content).toBe(1);
  });

  it.each([
    ['id', { message: message({ id: 2 }) }],
    ['content', { message: message({ content: 'hello world' }) }],
    ['status', { message: message({ status: 'loading' }) }],
    ['role', { message: message({ role: 'system' }) }],
    ['reasoningContent', { message: message({ reasoningContent: 'why' }) }],
    [
      'isReasoningExpanded',
      { message: message({ isReasoningExpanded: true }) },
    ],
    ['isEditing', { isEditing: true }],
    ['editValue', { editValue: 'draft' }],
    ['isMobile', { styleState: { isMobile: true } }],
  ])('re-renders when %s changes', (_label, change) => {
    const { rerender } = render(
      React.createElement(OptimizedMessageContent, props()),
    );
    rerender(React.createElement(OptimizedMessageContent, props(change)));
    expect(renders.content).toBe(2);
  });

  it('ignores props it was never told to watch', () => {
    const { rerender } = render(
      React.createElement(OptimizedMessageContent, props({ onCopy: () => 1 })),
    );
    rerender(
      React.createElement(OptimizedMessageContent, props({ onCopy: () => 2 })),
    );
    expect(renders.content).toBe(1);
  });
});

describe('OptimizedMessageActions', () => {
  const onMessageReset = () => {};
  const props = (over = {}) => ({
    message: message(),
    isAnyMessageGenerating: false,
    isEditing: false,
    onMessageReset,
    ...over,
  });

  it('holds still while nothing it watches changes', () => {
    const { rerender } = render(
      React.createElement(OptimizedMessageActions, props()),
    );
    rerender(React.createElement(OptimizedMessageActions, props()));
    expect(renders.actions).toBe(1);
  });

  it.each([
    ['id', { message: message({ id: 9 }) }],
    ['role', { message: message({ role: 'system' }) }],
    ['isAnyMessageGenerating', { isAnyMessageGenerating: true }],
    ['isEditing', { isEditing: true }],
    ['onMessageReset identity', { onMessageReset: () => {} }],
  ])('re-renders when %s changes', (_label, change) => {
    const { rerender } = render(
      React.createElement(OptimizedMessageActions, props()),
    );
    rerender(React.createElement(OptimizedMessageActions, props(change)));
    expect(renders.actions).toBe(2);
  });

  // DEFECT (correctness): this comparator watches only id / role /
  // isAnyMessageGenerating / isEditing / onMessageReset. `message.status` and
  // `message.content` are omitted — yet MessageActions decides its entire
  // button set from exactly those two (retry/edit/role/delete are hidden while
  // status is 'loading' or 'incomplete'; copy and edit are hidden when content
  // is empty). So a message that finishes generating, or that gains its first
  // text, keeps the action bar it had before. In the ordinary send flow
  // `isAnyMessageGenerating` flips at the same moment and masks this, but any
  // path that settles one message while others are still streaming — or that
  // fills in content without touching that flag — leaves the user with a reply
  // they cannot retry, copy or delete until something unrelated re-renders.
  // The pin that used to sit here asserted the render count stays at 1 across
  // that transition — it pinned the stale bar in place, so teaching the
  // comparator to watch status/content (the lock below) would have turned it
  // red. Replaced with the ordinary send flow, which must hold either side of
  // that fix: when a message settles, `isAnyMessageGenerating` flips with it,
  // and the bar that comes back has to be the bar for the SETTLED message.
  // Counting renders alone cannot see that — the stub records the props too.
  it('hands the settled message to the action bar on the ordinary send flow', () => {
    const streaming = {
      message: message({ status: 'loading', content: '' }),
      isAnyMessageGenerating: true,
    };
    const { rerender } = render(
      React.createElement(OptimizedMessageActions, props(streaming)),
    );
    expect(renders.actions).toBe(1);
    expect(lastProps.actions.message).toMatchObject({
      status: 'loading',
      content: '',
    });

    // Generation ends: the message completes, gains its text, and the global
    // "something is streaming" flag drops.
    rerender(
      React.createElement(
        OptimizedMessageActions,
        props({
          message: message({ status: 'complete', content: 'done' }),
          isAnyMessageGenerating: false,
        }),
      ),
    );
    expect(renders.actions).toBe(2);
    // Whatever made it re-render, MessageActions must be looking at the
    // finished message — otherwise retry/copy/delete act on a message the user
    // is no longer seeing.
    expect(lastProps.actions.message).toMatchObject({
      status: 'complete',
      content: 'done',
    });
    expect(lastProps.actions.isAnyMessageGenerating).toBe(false);
  });

  it.skip('a message that finishes generating must refresh its action bar', () => {
    const { rerender } = render(
      React.createElement(
        OptimizedMessageActions,
        props({ message: message({ status: 'loading', content: '' }) }),
      ),
    );
    rerender(
      React.createElement(
        OptimizedMessageActions,
        props({ message: message({ status: 'complete', content: 'done' }) }),
      ),
    );
    expect(renders.actions).toBe(2);
  });
});

describe('OptimizedSettingsPanel', () => {
  const props = (over = {}) => ({
    inputs: { model: 'gpt-4o' },
    parameterEnabled: { temperature: true },
    models: ['a'],
    groups: ['g'],
    customRequestMode: false,
    customRequestBody: '',
    showDebugPanel: false,
    showSettings: true,
    previewPayload: { messages: [] },
    messages: [],
    ...over,
  });

  it('compares its object props by value, not by identity', () => {
    const { rerender } = render(
      React.createElement(OptimizedSettingsPanel, props()),
    );
    // Fresh objects with identical contents — a shallow comparator would
    // re-render here; this one deliberately does not.
    rerender(React.createElement(OptimizedSettingsPanel, props()));
    expect(renders.settings).toBe(1);
  });

  it.each([
    ['inputs', { inputs: { model: 'claude' } }],
    ['parameterEnabled', { parameterEnabled: { temperature: false } }],
    ['models', { models: ['a', 'b'] }],
    ['groups', { groups: [] }],
    ['customRequestMode', { customRequestMode: true }],
    ['customRequestBody', { customRequestBody: '{}' }],
    ['showDebugPanel', { showDebugPanel: true }],
    ['showSettings', { showSettings: false }],
    ['previewPayload', { previewPayload: { messages: [{ a: 1 }] } }],
    ['messages', { messages: [{ id: 1 }] }],
  ])('re-renders when %s changes', (_label, change) => {
    const { rerender } = render(
      React.createElement(OptimizedSettingsPanel, props()),
    );
    rerender(React.createElement(OptimizedSettingsPanel, props(change)));
    expect(renders.settings).toBe(2);
  });
});

describe('OptimizedDebugPanel', () => {
  const props = (over = {}) => ({
    show: true,
    activeTab: 'preview',
    debugData: { request: '{}' },
    previewPayload: { model: 'gpt-4o' },
    customRequestMode: false,
    showDebugPanel: true,
    ...over,
  });

  it('holds still for structurally identical debug data', () => {
    const { rerender } = render(
      React.createElement(OptimizedDebugPanel, props()),
    );
    rerender(React.createElement(OptimizedDebugPanel, props()));
    expect(renders.debug).toBe(1);
  });

  it.each([
    ['show', { show: false }],
    ['activeTab', { activeTab: 'response' }],
    ['debugData', { debugData: { request: '{"a":1}' } }],
    ['previewPayload', { previewPayload: { model: 'claude' } }],
    ['customRequestMode', { customRequestMode: true }],
    ['showDebugPanel', { showDebugPanel: false }],
  ])('re-renders when %s changes', (_label, change) => {
    const { rerender } = render(
      React.createElement(OptimizedDebugPanel, props()),
    );
    rerender(React.createElement(OptimizedDebugPanel, props(change)));
    expect(renders.debug).toBe(2);
  });
});
