import type { ModelEntry } from './catalog';
import {
  WIRE_CHAT,
  WIRE_GENERATE,
  WIRE_MESSAGES,
  WIRE_RESPONSES,
} from './wire';

const STATE = 'Help! My payouts have been failing for 3 days.';
const QUESTION = 'Does this convey urgency?';
const SYSTEM_ONE = 'systemone';

type Snippet = Pick<ModelEntry, 'id' | 'capabilities'>;

export type SnippetLang = 'curl' | 'python' | 'typescript';

export const SNIPPET_LANGS: readonly SnippetLang[] = [
  'curl',
  'python',
  'typescript',
];

export const SNIPPET_LANG_LABEL: Record<SnippetLang, string> = {
  curl: 'cURL',
  python: 'Python',
  typescript: 'TypeScript',
};

/** Only reachable through POST /v1/systemone. */
export const isSystemOneOnly = (e: Snippet): boolean =>
  e.capabilities.includes(SYSTEM_ONE) && !e.capabilities.includes(WIRE_CHAT);

/** Capability ids a ready-made request exists for, in display order. */
const SNIPPET_ENDPOINTS = [
  WIRE_CHAT,
  WIRE_RESPONSES,
  WIRE_MESSAGES,
  WIRE_GENERATE,
  'embeddings',
  'image-generation',
  'jina-rerank',
  SYSTEM_ONE,
] as const;

/**
 * Endpoints worth showing a request for. A model whose endpoints are unknown
 * (none reported) gets the chat request, as the relay's default.
 */
export function snippetEndpoints(e: Snippet): string[] {
  const own = SNIPPET_ENDPOINTS.filter((c) => e.capabilities.includes(c));
  return own.length > 0 ? [...own] : [WIRE_CHAT];
}

interface Request {
  method: 'POST';
  /** Path after the origin, with the model already substituted. */
  path: string;
  headers: Array<[string, string]>;
  body: unknown;
}

const AUTH: [string, string] = ['Authorization', 'Bearer $LURUS_API_KEY'];
const JSON_TYPE: [string, string] = ['Content-Type', 'application/json'];

function requestFor(e: Snippet, endpoint: string): Request {
  const id = e.id;
  switch (endpoint) {
    case WIRE_RESPONSES:
      return {
        method: 'POST',
        path: '/v1/responses',
        headers: [AUTH, JSON_TYPE],
        body: { model: id, input: 'hello' },
      };
    case WIRE_MESSAGES:
      return {
        method: 'POST',
        path: '/v1/messages',
        headers: [
          ['x-api-key', '$LURUS_API_KEY'],
          JSON_TYPE,
        ],
        body: {
          model: id,
          max_tokens: 1024,
          messages: [{ role: 'user', content: 'hello' }],
        },
      };
    case WIRE_GENERATE:
      return {
        method: 'POST',
        path: `/v1beta/models/${id}:generateContent`,
        headers: [['x-goog-api-key', '$LURUS_API_KEY'], JSON_TYPE],
        body: { contents: [{ parts: [{ text: 'hello' }] }] },
      };
    case 'embeddings':
      return {
        method: 'POST',
        path: '/v1/embeddings',
        headers: [AUTH, JSON_TYPE],
        body: { model: id, input: 'hello' },
      };
    case 'image-generation':
      return {
        method: 'POST',
        path: '/v1/images/generations',
        headers: [AUTH, JSON_TYPE],
        body: { model: id, prompt: 'a lighthouse at dusk', n: 1 },
      };
    case 'jina-rerank':
      return {
        method: 'POST',
        path: '/v1/rerank',
        headers: [AUTH, JSON_TYPE],
        body: {
          model: id,
          query: 'hello',
          documents: ['hello world', 'goodbye'],
        },
      };
    case SYSTEM_ONE:
      return {
        method: 'POST',
        path: '/v1/systemone',
        headers: [AUTH, JSON_TYPE],
        body: {
          model: id,
          state: STATE,
          questions: { is_urgent: { type: 'noul', instructions: QUESTION } },
        },
      };
    default:
      return {
        method: 'POST',
        path: '/v1/chat/completions',
        headers: [AUTH, JSON_TYPE],
        body: {
          model: id,
          messages: [{ role: 'user', content: 'hello' }],
        },
      };
  }
}

const compact = (v: unknown) => JSON.stringify(v);

function curl(r: Request, base: string): string {
  const lines = [`curl ${base}${r.path} \\`];
  for (const [k, v] of r.headers) lines.push(`  -H "${k}: ${v}" \\`);
  // Spaces after ":" and "," keep the single-line body readable.
  const body = compact(r.body)
    .replaceAll('":', '": ')
    .replaceAll(',"', ', "');
  lines.push(`  -d '${body}'`);
  return lines.join('\n');
}

function python(r: Request, base: string): string {
  const headers = r.headers
    .map(([k, v]) =>
      v.includes('$LURUS_API_KEY')
        ? `        "${k}": ${
            v.startsWith('Bearer ')
              ? 'f"Bearer {os.environ[\'LURUS_API_KEY\']}"'
              : "os.environ['LURUS_API_KEY']"
          },`
        : `        "${k}": "${v}",`,
    )
    .join('\n');
  return `import os
import requests

resp = requests.post(
    "${base}${r.path}",
    headers={
${headers}
    },
    json=${JSON.stringify(r.body, null, 4).replaceAll('\n', '\n    ')},
)
print(resp.json())`;
}

function typescript(r: Request, base: string): string {
  const headers = r.headers
    .map(([k, v]) =>
      v.includes('$LURUS_API_KEY')
        ? `    '${k}': ${
            v.startsWith('Bearer ')
              ? '`Bearer ${process.env.LURUS_API_KEY}`'
              : 'process.env.LURUS_API_KEY!'
          },`
        : `    '${k}': '${v}',`,
    )
    .join('\n');
  return `const resp = await fetch('${base}${r.path}', {
  method: 'POST',
  headers: {
${headers}
  },
  body: JSON.stringify(${JSON.stringify(r.body, null, 2).replaceAll('\n', '\n  ')}),
});
console.log(await resp.json());`;
}

/** Copy-paste request for one endpoint in one language. */
export function buildSnippet(
  e: Snippet,
  endpoint: string,
  lang: SnippetLang,
  base: string,
): string {
  const r = requestFor(e, endpoint);
  if (lang === 'python') return python(r, base);
  if (lang === 'typescript') return typescript(r, base);
  return curl(r, base);
}

export function curlSnippet(e: Snippet, base: string): string {
  return buildSnippet(e, snippetEndpoints(e)[0], 'curl', base);
}

export function pythonSnippet(e: Snippet, base: string): string {
  return buildSnippet(e, snippetEndpoints(e)[0], 'python', base);
}
