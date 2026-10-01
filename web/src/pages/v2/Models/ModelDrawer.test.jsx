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
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// Vendor logos come from a ~4 MB pack loaded on first render (same stub as
// Marketplace.test.jsx).
vi.mock('@lobehub/icons', () => ({}));

import ModelDrawer, { CAPABILITY_PATH, capLabel } from './ModelDrawer';
import Marketplace from './Marketplace';
import en from '../../../i18n/locales/en.json';
import zh from '../../../i18n/locales/zh.json';

const tr = (key, fallback) => (typeof fallback === 'string' ? fallback : key);
const BASE = 'https://hub.example.test';

const entry = (id, capabilities, overrides = {}) => ({
  id,
  vendor: 'TypeSafe',
  description: '',
  tags: [],
  capabilities,
  routable: true,
  priced: true,
  quotaType: 0,
  inputPerM: 0.084,
  outputPerM: 0,
  cacheReadPerM: null,
  perCall: null,
  tokens: 0,
  requests: 0,
  catalogueId: null,
  status: null,
  p50Ms: null,
  p95Ms: null,
  errorRate: null,
  enoughSamples: false,
  ...overrides,
});

const renderDrawer = (e) =>
  render(
    <ModelDrawer
      e={e}
      tr={tr}
      onClose={() => {}}
      onTry={() => {}}
      onKeys={() => {}}
      onCopy={() => {}}
      base={BASE}
      availability={null}
    />,
  );

describe('System One capability', () => {
  it('has a label and a relay path', () => {
    expect(capLabel(tr, 'systemone')).toBe('System One');
    expect(CAPABILITY_PATH.systemone).toBe('POST /v1/systemone');
  });

  it.each([
    ['en', en],
    ['zh', zh],
  ])('%s carries the capability label key', (_, { translation }) => {
    expect(translation.console.models.market.cap_systemone).toBeTruthy();
  });

  it('the drawer lists the capability with its endpoint', () => {
    renderDrawer(entry('jev-latest', ['systemone']));
    const drawer = screen.getByTestId('model-drawer');
    expect(drawer.textContent).toContain('System One');
    expect(drawer.textContent).toContain('POST /v1/systemone');
  });
});

describe('System One quick start', () => {
  it('cURL targets /v1/systemone with a noul question, never chat completions', () => {
    renderDrawer(entry('jev-latest', ['systemone']));
    const code = screen.getByTestId('model-drawer-code').textContent;
    expect(code).toContain(`curl ${BASE}/v1/systemone`);
    expect(code).toContain('Authorization: Bearer $LURUS_API_KEY');
    expect(code).toContain('"model": "jev-latest"');
    expect(code).toContain('"state"');
    expect(code).toContain('"type": "noul"');
    expect(code).not.toContain('chat/completions');
    expect(code).not.toContain('"messages"');
    // The line continuations survive the template literal.
    const lines = code.split('\n');
    expect(lines).toHaveLength(4);
    expect(lines[0].endsWith(' \\')).toBe(true);
  });

  it('the snippet is valid JSON apart from the shell quoting', () => {
    renderDrawer(entry('laya-english', ['systemone']));
    const code = screen.getByTestId('model-drawer-code').textContent;
    const body = JSON.parse(code.slice(code.indexOf("-d '") + 4, -1));
    expect(body.model).toBe('laya-english');
    expect(body.questions.is_urgent.type).toBe('noul');
  });

  it('Python uses the official SDK pointed at the hub', () => {
    renderDrawer(entry('jev-latest', ['systemone']));
    fireEvent.click(screen.getByText('Python'));
    const code = screen.getByTestId('model-drawer-code').textContent;
    expect(code).toContain('pip install typesafe-sdk');
    expect(code).toContain(`TYPESAFE_BASE_URL=${BASE}`);
    expect(code).toContain('TYPESAFE_API_KEY=');
    expect(code).toContain('from typesafe_sdk import');
    expect(code).toContain('TypeSafeClient(model="jev-latest")');
    expect(code).toContain('client.system_one(');
    expect(code).not.toContain('openai');
    expect(code).not.toContain('chat.completions');
  });

  it('a chat model keeps the chat snippet', () => {
    renderDrawer(entry('rt-alpha', ['openai']));
    expect(screen.getByTestId('model-drawer-code').textContent).toContain(
      '/v1/chat/completions',
    );
  });
});

// The playground only runs chat completions: a System One model there would
// be dropped as "not routable".
describe('try-in-playground for non-chat models', () => {
  it('the drawer offers no playground button for a System One only model', () => {
    renderDrawer(entry('jev-latest', ['systemone']));
    expect(screen.queryByTestId('model-drawer-try')).toBeNull();
  });

  it('the drawer keeps it when chat is among the capabilities or unknown', () => {
    const { unmount } = renderDrawer(entry('rt-alpha', ['openai']));
    expect(screen.getByTestId('model-drawer-try')).toBeInTheDocument();
    unmount();
    renderDrawer(entry('rt-bare', []));
    expect(screen.getByTestId('model-drawer-try')).toBeInTheDocument();
  });

  it('the card offers none either', () => {
    render(
      <Marketplace
        entries={[
          entry('jev-latest', ['systemone']),
          entry('rt-alpha', ['openai']),
        ]}
        loading={false}
        onTry={() => {}}
        onKeys={() => {}}
        onCopy={() => {}}
        base={BASE}
        tr={tr}
      />,
    );
    expect(screen.queryByTestId('model-try-jev-latest')).toBeNull();
    expect(screen.getByTestId('model-try-rt-alpha')).toBeInTheDocument();
  });
});
