/*
Copyright (C) 2023-2026 QuantumNous

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
import { describe, expect, it } from 'vitest';

import {
  formatLatency,
  formatSuccessRate,
  getSuccessRateLevel,
  successRateOf,
} from './perf';
import {
  buildSnippet,
  SNIPPET_LANGS,
  snippetEndpoints,
  type SnippetLang,
} from './snippets';
import { WIRE_CHAT, WIRE_GENERATE, WIRE_MESSAGES, WIRE_VIDEO } from './wire';

describe('success rate helpers', () => {
  it('is a measurement only with enough samples and a sane error rate', () => {
    expect(successRateOf({ errorRate: 0.02, enoughSamples: true })).toBeCloseTo(
      98,
    );
    expect(successRateOf({ errorRate: 0.02, enoughSamples: false })).toBeNull();
    expect(successRateOf({ errorRate: null, enoughSamples: true })).toBeNull();
    expect(successRateOf({ errorRate: 1.5, enoughSamples: true })).toBeNull();
    expect(successRateOf({ errorRate: -0.1, enoughSamples: true })).toBeNull();
  });

  it('grades and formats rates, with a dash when unknown', () => {
    expect(getSuccessRateLevel(100)).toBe('excellent');
    expect(getSuccessRateLevel(99.5)).toBe('good');
    expect(getSuccessRateLevel(96)).toBe('warning');
    expect(getSuccessRateLevel(50)).toBe('critical');
    expect(getSuccessRateLevel(null)).toBe('unknown');
    expect(formatSuccessRate(99.044)).toBe('99.04%');
    expect(formatSuccessRate(null)).toBe('—');
  });

  it('formats latency as ms or seconds and never as zero', () => {
    expect(formatLatency(850)).toBe('850ms');
    expect(formatLatency(1500)).toBe('1.50s');
    expect(formatLatency(0)).toBe('—');
    expect(formatLatency(null)).toBe('—');
    expect(formatLatency(Number.NaN)).toBe('—');
  });
});

describe('request snippets', () => {
  const chat = { id: 'model-a', capabilities: [WIRE_CHAT, WIRE_MESSAGES] };

  it('offers only the endpoints the model reports, chat when unknown', () => {
    expect(snippetEndpoints(chat)).toEqual([WIRE_CHAT, WIRE_MESSAGES]);
    expect(snippetEndpoints({ id: 'x', capabilities: [] })).toEqual([
      WIRE_CHAT,
    ]);
    expect(
      snippetEndpoints({ id: 'x', capabilities: ['jina-rerank', WIRE_VIDEO] }),
    ).toEqual(['jina-rerank']);
  });

  it('builds every language for every endpoint without leftovers', () => {
    for (const ep of [
      WIRE_CHAT,
      WIRE_MESSAGES,
      WIRE_GENERATE,
      'embeddings',
      'image-generation',
      'jina-rerank',
      'systemone',
    ]) {
      for (const lang of SNIPPET_LANGS as readonly SnippetLang[]) {
        const code = buildSnippet(
          { id: 'model-a', capabilities: [ep] },
          ep,
          lang,
          'https://hub.example',
        );
        expect(code).toContain('https://hub.example/');
        expect(code).toContain('model-a');
        expect(code).not.toContain('undefined');
      }
    }
  });

  it('puts the model in the path for the generate-content endpoint', () => {
    const code = buildSnippet(
      { id: 'model-a', capabilities: [WIRE_GENERATE] },
      WIRE_GENERATE,
      'curl',
      'https://hub.example',
    );
    expect(code).toContain('/v1beta/models/model-a:generateContent');
    expect(code).toContain('x-goog-api-key');
  });

  it('reads the key from the environment instead of embedding one', () => {
    const py = buildSnippet(chat, WIRE_CHAT, 'python', 'https://h');
    expect(py).toContain("os.environ['LURUS_API_KEY']");
    const ts = buildSnippet(chat, WIRE_CHAT, 'typescript', 'https://h');
    expect(ts).toContain('process.env.LURUS_API_KEY');
  });
});
