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

// buildSnippets — pulled out of Token/index.jsx (cycle-19 L3) so
// ViewCodePanel.jsx (Playground "view code") can build the exact same curl
// / python / node text this page already ships to customers, instead of
// hand-rolling a second, divergent copy.
//
// `host` is the live relay host (Token/index.jsx's relayHostFromServer) —
// never a literal. Every snippet below is copy-pasted verbatim by
// customers, so a stale host here is a broken integration, not a cosmetic
// issue.
//
// openaiModel/anthropicModel come from the routing truth
// (useRoutableModels, narrowed to the caller's own scope) — never a literal
// vendor model name. anthropicModel may be undefined — the anthropic
// snippet then never renders (langTabsFor hides its tab to match).
export const buildSnippets = (
  key,
  host,
  { openaiModel, anthropicModel, system, user, params } = {},
) => {
  // No caller passed the third argument at all — every existing caller
  // (Token/index.jsx) is this branch, and its output must stay byte-for-byte
  // what it was before this function moved out of that file.
  if (system == null && user == null && params == null) {
    return {
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
    };
  }

  // Extended shape (ViewCodePanel / Playground "view code"): the caller's
  // own system prompt, user message and sampling params, built through
  // JSON.stringify — not string interpolation — so a user message
  // containing quotes or newlines round-trips as valid JSON rather than
  // breaking the literal it is embedded in.
  const openaiMessages = [];
  if (system) openaiMessages.push({ role: 'system', content: system });
  openaiMessages.push({ role: 'user', content: user ?? '' });

  const body = { model: openaiModel, messages: openaiMessages };
  if (params?.temperature != null) body.temperature = params.temperature;
  if (params?.top_p != null) body.top_p = params.top_p;
  if (params?.max_tokens != null) body.max_tokens = params.max_tokens;

  // Compact for curl's single -d string; a literal ' inside it would
  // otherwise close the shell's single-quoted string early — '\'' closes
  // the quote, emits an escaped literal quote, then reopens it, which is
  // the standard POSIX-shell idiom for "a single quote inside single
  // quotes".
  const curlBody = JSON.stringify(body).replace(/'/g, `'\\''`);
  // Pretty JSON is also valid Python (str/int/float/list/dict all share
  // JSON's literal syntax) and valid JS, so the same string doubles as both
  // languages' kwargs/object-literal argument.
  const bodyLiteral = JSON.stringify(body, null, 4);

  return {
    curl: `curl ${host}/v1/chat/completions \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '${curlBody}'`,
    python: `from openai import OpenAI

client = OpenAI(api_key="${key}", base_url="${host}/v1")
resp = client.chat.completions.create(**${bodyLiteral})
print(resp.choices[0].message.content)`,
    node: `import OpenAI from "openai";

const client = new OpenAI({ apiKey: "${key}", baseURL: "${host}/v1" });
const r = await client.chat.completions.create(${bodyLiteral});`,
    anthropic: anthropicModel
      ? `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  apiKey: "${key}",
  baseURL: "${host}",
});
await client.messages.create(${JSON.stringify(
          {
            model: anthropicModel,
            max_tokens: params?.max_tokens ?? 1024,
            ...(system ? { system } : {}),
            messages: [{ role: 'user', content: user ?? '' }],
          },
          null,
          4,
        )});`
      : '',
  };
};

// langTabsFor drops the Anthropic SDK tab when no routable (token-scoped)
// model speaks the anthropic wire — showing a tab whose snippet embeds
// "model":"undefined" would be worse than not offering it.
export const ALL_LANG_TABS = [
  ['curl', 'cURL'],
  ['python', 'Python'],
  ['node', 'Node.js'],
  ['anthropic', 'Anthropic SDK'],
];
export const langTabsFor = (anthropicModel) =>
  anthropicModel
    ? ALL_LANG_TABS
    : ALL_LANG_TABS.filter(([k]) => k !== 'anthropic');
