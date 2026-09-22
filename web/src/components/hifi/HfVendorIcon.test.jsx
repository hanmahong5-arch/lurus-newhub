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
import { render, screen } from '@testing-library/react';

vi.mock('@lobehub/icons', () => ({}));

import HfVendorIcon, { vendorIconFor } from './HfVendorIcon';

describe('vendorIconFor', () => {
  it.each([
    ['deepseek-chat', 'DeepSeek'],
    ['gpt-4o-mini', 'OpenAI'],
    ['o3-mini', 'OpenAI'],
    ['claude-sonnet-4-5', 'Claude'],
    ['gemini-2.5-pro', 'Gemini'],
    ['qwen3-max', 'Qwen'],
    ['glm-4.6', 'Zhipu'],
    ['doubao-seed-1.6', 'Doubao'],
    ['deepseek-ai/DeepSeek-V3', 'DeepSeek'],
    ['mj_imagine', 'Midjourney'],
  ])('maps model %s to %s', (model, name) => {
    expect(vendorIconFor({ model })?.name).toBe(name);
  });

  it('prefers the vendor name over the model name', () => {
    expect(
      vendorIconFor({ model: 'my-model', vendor: 'Anthropic' })?.name,
    ).toBe('Claude');
  });

  it.each([
    ['Anthropic Claude', 'Claude'],
    ['Google Gemini', 'Gemini'],
    ['Suno API', 'Suno'],
    ['Azure OpenAI', 'Azure'],
  ])('maps channel-type label %s to %s', (vendor, name) => {
    expect(vendorIconFor({ vendor })?.name).toBe(name);
  });

  it('returns null for an unknown model and vendor', () => {
    expect(vendorIconFor({ model: 'faultsim-music' })).toBeNull();
  });
});

describe('HfVendorIcon', () => {
  it('renders the pack icon when one is known', () => {
    render(<HfVendorIcon model='deepseek-chat' />);
    expect(screen.getByTestId('hf-vendor-icon').dataset.icon).toBe('DeepSeek');
  });

  it('renders a lettered tile, not an empty gap, when none is known', () => {
    render(<HfVendorIcon model='faultsim-music' />);
    const el = screen.getByTestId('hf-vendor-icon');
    expect(el.dataset.icon).toBe('');
    expect(el.textContent).toBe('F');
  });
});
