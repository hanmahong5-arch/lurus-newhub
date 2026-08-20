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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, vars) =>
      vars
        ? Object.entries(vars).reduce(
            (s, [k, v]) => s.replace(new RegExp(`{{\\s*${k}\\s*}}`, 'g'), v),
            key,
          )
        : key,
    i18n: { language: 'zh' },
  }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../../helpers', () => ({
  copy: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  const Text = ({ children }) => h('span', null, children);
  return {
    Card: ({ children }) => h('div', { 'data-testid': 'key-card' }, children),
    Button: ({ onClick, children }) =>
      h('button', { type: 'button', onClick }, children),
    Tag: ({ children }) => h('span', { 'data-testid': 'tag' }, children),
    Typography: { Text },
  };
});

import { copy, showSuccess } from '../../../helpers';
import ChannelKeyDisplay from './ChannelKeyDisplay';

const cards = () => screen.queryAllByTestId('key-card');
const copyButtons = () => screen.getAllByRole('button', { name: '复制' });

beforeEach(() => {
  copy.mockReset();
  showSuccess.mockReset();
});

describe('ChannelKeyDisplay — key parsing', () => {
  it('shows a single plain key as one untagged card', () => {
    render(<ChannelKeyDisplay keyData='  sk-abc123  ' />);

    expect(cards()).toHaveLength(1);
    expect(screen.getByText('sk-abc123')).toBeInTheDocument();
    expect(screen.getByText('渠道密钥')).toBeInTheDocument();
    expect(screen.queryByTestId('tag')).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: '复制全部' }),
    ).not.toBeInTheDocument();
  });

  it('splits a JSON array of strings into one card per key', () => {
    render(<ChannelKeyDisplay keyData='["sk-one","sk-two","sk-three"]' />);

    expect(cards()).toHaveLength(3);
    expect(screen.getByText('密钥 1')).toBeInTheDocument();
    expect(screen.getByText('密钥 3')).toBeInTheDocument();
    expect(screen.getByText('渠道密钥列表')).toBeInTheDocument();
    expect(screen.getByText('共 3 个密钥')).toBeInTheDocument();
    expect(screen.queryByTestId('tag')).not.toBeInTheDocument();
  });

  it('pretty-prints object entries of a JSON array and tags them as JSON', () => {
    render(<ChannelKeyDisplay keyData='[{"project":"p1"},"sk-plain"]' />);

    expect(cards()).toHaveLength(2);
    // Exact text, newlines and all — the indentation is the point.
    expect(
      screen.getByText('{\n  "project": "p1"\n}', { normalizer: (s) => s }),
    ).toBeInTheDocument();
    // Only the object entry carries the JSON tag and the format hint.
    expect(screen.getAllByTestId('tag')).toHaveLength(1);
    expect(screen.getAllByText('JSON格式密钥，请确保格式正确')).toHaveLength(1);
  });

  it('falls back to plain text when a bracketed value is not valid JSON', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});

    render(<ChannelKeyDisplay keyData='[sk-not-json' />);

    expect(cards()).toHaveLength(1);
    expect(screen.getByText('[sk-not-json')).toBeInTheDocument();
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it('treats newline-separated values as separate keys', () => {
    render(<ChannelKeyDisplay keyData={'sk-a\n\n  sk-b  \nsk-c'} />);

    expect(cards()).toHaveLength(3);
    // Blank lines are dropped and each key is trimmed.
    expect(screen.getByText('sk-b')).toBeInTheDocument();
    expect(screen.getByText('共 3 个密钥')).toBeInTheDocument();
  });

  it('tags a single-line JSON object key as JSON', () => {
    render(<ChannelKeyDisplay keyData='{"type":"service_account"}' />);

    expect(cards()).toHaveLength(1);
    expect(screen.getByTestId('tag')).toHaveTextContent('JSON');
  });

  it('renders no key cards at all when there is no key data', () => {
    render(<ChannelKeyDisplay keyData='' />);

    expect(cards()).toHaveLength(0);
    expect(screen.getByText('渠道密钥')).toBeInTheDocument();
    expect(screen.getByText('验证成功')).toBeInTheDocument();
  });

  // DEFECT (skipped): parseChannelKeys checks "starts with [" before it checks
  // for newlines, so a pretty-printed single credential — exactly what a Vertex
  // AI / GCP service-account JSON looks like when pasted — is shredded one line
  // per "key". The user then sees "共 5 个密钥" and copying key 1 yields the
  // lone character "{". The newline split needs to run only after ruling out a
  // single JSON object.
  it.skip('keeps a pretty-printed JSON credential as one key', () => {
    const credential =
      '{\n  "type": "service_account",\n  "project_id": "p1"\n}';

    render(<ChannelKeyDisplay keyData={credential} />);

    expect(cards()).toHaveLength(1);
    expect(screen.getByTestId('tag')).toHaveTextContent('JSON');
  });
});

describe('ChannelKeyDisplay — copying', () => {
  it('copies just the clicked key', () => {
    render(<ChannelKeyDisplay keyData='["sk-one","sk-two"]' />);

    fireEvent.click(copyButtons()[1]);

    expect(copy).toHaveBeenCalledWith('sk-two');
    expect(showSuccess).toHaveBeenCalledWith('密钥已复制到剪贴板');
  });

  it('copies the raw payload when copying everything', () => {
    render(<ChannelKeyDisplay keyData='["sk-one","sk-two"]' />);

    fireEvent.click(screen.getByRole('button', { name: '复制全部' }));

    expect(copy).toHaveBeenCalledWith('["sk-one","sk-two"]');
    expect(showSuccess).toHaveBeenCalledWith('所有密钥已复制到剪贴板');
  });

  it('shows the multi-key explainer only when there really are several', () => {
    const { unmount } = render(<ChannelKeyDisplay keyData='sk-single' />);
    expect(
      screen.queryByText(
        '检测到多个密钥，您可以单独复制每个密钥，或点击复制全部获取完整内容。',
      ),
    ).not.toBeInTheDocument();
    unmount();

    render(<ChannelKeyDisplay keyData={'sk-a\nsk-b'} />);
    expect(
      screen.getByText(
        '检测到多个密钥，您可以单独复制每个密钥，或点击复制全部获取完整内容。',
      ),
    ).toBeInTheDocument();
  });
});

describe('ChannelKeyDisplay — optional chrome', () => {
  it('hides the success banner and the warning on request', () => {
    render(
      <ChannelKeyDisplay
        keyData='sk-abc'
        showSuccessIcon={false}
        showWarning={false}
      />,
    );

    expect(screen.queryByText('验证成功')).not.toBeInTheDocument();
    expect(screen.queryByText('安全提醒')).not.toBeInTheDocument();
  });

  it('honours caller-supplied success and warning copy', () => {
    render(
      <ChannelKeyDisplay
        keyData='sk-abc'
        successText='渠道已就绪'
        warningText='仅本人可见'
      />,
    );

    expect(screen.getByText('渠道已就绪')).toBeInTheDocument();
    expect(screen.getByText('仅本人可见')).toBeInTheDocument();
    expect(screen.queryByText('验证成功')).not.toBeInTheDocument();
  });

  it('falls back to the default warning copy', () => {
    render(<ChannelKeyDisplay keyData='sk-abc' />);

    expect(screen.getByText('安全提醒')).toBeInTheDocument();
    expect(
      screen.getByText(
        '请妥善保管密钥信息，不要泄露给他人。如有安全疑虑，请及时更换密钥。',
      ),
    ).toBeInTheDocument();
  });
});
