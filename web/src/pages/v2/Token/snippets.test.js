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
import { buildSnippets, langTabsFor, ALL_LANG_TABS } from './snippets';

// The exact byte shape Token/index.jsx shipped before buildSnippets moved
// into this file — every existing caller omits the third argument, and
// that output must not shift by even a byte.
const preExtractionSnapshot = (key, host, openaiModel, anthropicModel) => ({
  curl: `curl ${host}/v1/chat/completions \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${openaiModel}","messages":[{"role":"user","content":"hi"}]}'`,
  python: `from openai import OpenAI

client = OpenAI(api_key="${key}", base_url="${host}/v1")
resp = client.chat.completions.create(
    model="${openaiModel}",
    messages=[{"role": "user", "content": "hi"}],
)
print(resp.choices[0].message.content)`,
  node: `import OpenAI from "openai";

const client = new OpenAI({ apiKey: "${key}", baseURL: "${host}/v1" });
const r = await client.chat.completions.create({
  model: "${openaiModel}",
  messages: [{ role: "user", content: "hi" }],
});`,
  anthropic: anthropicModel
    ? `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: "${key}",
  baseURL: "${host}",
});
await client.messages.create({
  model: "${anthropicModel}",
  max_tokens: 1024,
  messages: [{ role: "user", content: "hi" }],
});`
    : '',
});

describe('buildSnippets', () => {
  it('matches the pre-extraction output byte-for-byte with no third argument', () => {
    const got = buildSnippets('sk-abc123', 'https://hub.lurus.cn', {
      openaiModel: 'model-a',
      anthropicModel: 'model-b',
    });
    const want = preExtractionSnapshot(
      'sk-abc123',
      'https://hub.lurus.cn',
      'model-a',
      'model-b',
    );
    expect(got.curl).toBe(want.curl);
    expect(got.python).toBe(want.python);
    expect(got.node).toBe(want.node);
    expect(got.anthropic).toBe(want.anthropic);
  });

  it('matches byte-for-byte when there is no anthropic-wire model either', () => {
    const got = buildSnippets('YOUR_KEY', 'https://hub.lurus.cn', {
      openaiModel: 'model-a',
    });
    const want = preExtractionSnapshot(
      'YOUR_KEY',
      'https://hub.lurus.cn',
      'model-a',
      undefined,
    );
    expect(got).toEqual(want);
    expect(got.anthropic).toBe('');
  });

  it('the model name always comes from the argument, never a literal', () => {
    const got = buildSnippets('k', 'https://h', { openaiModel: 'rt-custom-1' });
    expect(got.curl).toContain('"model":"rt-custom-1"');
    expect(got.python).toContain('model="rt-custom-1"');
    expect(got.node).toContain('model: "rt-custom-1"');
  });

  describe('extended form (system/user/params)', () => {
    it('closes the curl shell quote and stays valid JSON when the user message has a single quote and a newline', () => {
      const got = buildSnippets('sk-x', 'https://hub.lurus.cn', {
        openaiModel: 'model-a',
        user: "it's a test\nsecond line",
      });

      // Extract the -d '...' payload the way a shell actually would: find
      // the single-quoted argument, undoing the '\'' escape.
      const match = got.curl.match(/-d '([\s\S]*)'$/);
      expect(match).toBeTruthy();
      const shellUnescaped = match[1].replace(/'\\''/g, "'");
      expect(() => JSON.parse(shellUnescaped)).not.toThrow();
      const parsed = JSON.parse(shellUnescaped);
      expect(parsed.messages[0].content).toBe("it's a test\nsecond line");
      expect(parsed.model).toBe('model-a');
    });

    it('includes a system message only when provided', () => {
      const withSystem = buildSnippets('k', 'https://h', {
        openaiModel: 'm',
        system: 'be terse',
        user: 'hello',
      });
      expect(withSystem.node).toContain('"role": "system"');
      expect(withSystem.node).toContain('"content": "be terse"');

      const withoutSystem = buildSnippets('k', 'https://h', {
        openaiModel: 'm',
        user: 'hello',
      });
      expect(withoutSystem.node).not.toContain('"role": "system"');
    });

    it('carries temperature/top_p/max_tokens into the request body', () => {
      const got = buildSnippets('k', 'https://h', {
        openaiModel: 'm',
        user: 'hi',
        params: { temperature: 0.5, top_p: 0.9, max_tokens: 256 },
      });
      expect(got.node).toContain('"temperature": 0.5');
      expect(got.node).toContain('"top_p": 0.9');
      expect(got.node).toContain('"max_tokens": 256');
    });

    it('YOUR_KEY placeholder is used verbatim, never a real key baked in by this function', () => {
      const got = buildSnippets('YOUR_KEY', 'https://h', {
        openaiModel: 'm',
        user: 'hi',
      });
      expect(got.curl).toContain('YOUR_KEY');
      expect(got.curl).not.toMatch(/sk-[a-zA-Z0-9]{10,}/);
    });
  });
});

describe('langTabsFor', () => {
  it('includes the anthropic tab only when a model is given', () => {
    expect(langTabsFor('model-b')).toEqual(ALL_LANG_TABS);
    expect(langTabsFor(undefined).some(([k]) => k === 'anthropic')).toBe(false);
  });
});
