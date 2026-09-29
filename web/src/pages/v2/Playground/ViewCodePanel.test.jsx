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
import { fireEvent, render, screen } from '@testing-library/react';

// The '../../../helpers' barrel sits behind helpers/utils.jsx, which pulls
// in Semi UI's Toast (and, transitively, lottie-web) purely for import-time
// side effects — unrelated to anything this panel renders, and lottie-web
// crashes in jsdom's canvas-less environment. Every page test in this
// codebase sidesteps that by mocking the barrel rather than importing it
// for real.
vi.mock('../../../helpers', () => ({
  getServerAddress: () => 'https://hub.lurus.cn',
}));

import ViewCodePanel from './ViewCodePanel';

const baseProps = {
  model: 'rt-alpha',
  system: 'be terse',
  user: 'what is the capital of Australia?',
  params: { temperature: 0.7, top_p: 1, max_tokens: 1024 },
};

describe('ViewCodePanel', () => {
  it('defaults to the curl tab and shows the given model in the snippet', () => {
    render(<ViewCodePanel {...baseProps} />);
    const snippet = screen.getByTestId('view-code-snippet');
    expect(snippet.textContent).toContain('curl');
    expect(snippet.textContent).toContain('rt-alpha');
  });

  it('switching tabs shows a different snippet for each language', () => {
    render(<ViewCodePanel {...baseProps} />);
    const curlText = screen.getByTestId('view-code-snippet').textContent;

    fireEvent.click(screen.getByTestId('view-code-tab-python'));
    const pyText = screen.getByTestId('view-code-snippet').textContent;
    expect(pyText).not.toBe(curlText);
    expect(pyText).toContain('from openai import OpenAI');

    fireEvent.click(screen.getByTestId('view-code-tab-node'));
    const nodeText = screen.getByTestId('view-code-snippet').textContent;
    expect(nodeText).not.toBe(pyText);
    expect(nodeText).toContain('import OpenAI from "openai"');
  });

  it('never embeds a real key — only the YOUR_KEY placeholder, in every tab', () => {
    render(<ViewCodePanel {...baseProps} />);
    for (const lang of ['curl', 'python', 'node']) {
      fireEvent.click(screen.getByTestId(`view-code-tab-${lang}`));
      const text = screen.getByTestId('view-code-snippet').textContent;
      expect(text).toContain('YOUR_KEY');
      expect(text).not.toMatch(/sk-[a-zA-Z0-9]{10,}/);
    }
  });

  it('shows no anthropic tab when the model does not speak the anthropic wire', () => {
    render(<ViewCodePanel {...baseProps} />);
    expect(screen.queryByTestId('view-code-tab-anthropic')).toBeNull();
  });

  it('shows an anthropic tab, whose snippet uses the given model, when anthropicModel is set', () => {
    render(<ViewCodePanel {...baseProps} anthropicModel='rt-alpha' />);
    fireEvent.click(screen.getByTestId('view-code-tab-anthropic'));
    const text = screen.getByTestId('view-code-snippet').textContent;
    expect(text).toContain('@anthropic-ai/sdk');
    expect(text).toContain('rt-alpha');
  });

  it('carries the given system prompt and user message into the snippet', () => {
    render(<ViewCodePanel {...baseProps} />);
    fireEvent.click(screen.getByTestId('view-code-tab-node'));
    const text = screen.getByTestId('view-code-snippet').textContent;
    expect(text).toContain('be terse');
    expect(text).toContain('what is the capital of Australia?');
  });
});
