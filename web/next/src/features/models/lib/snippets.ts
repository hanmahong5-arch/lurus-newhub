import type { ModelEntry } from './catalog';
import { WIRE_CHAT } from './wire';

const STATE = 'Help! My payouts have been failing for 3 days.';
const QUESTION = 'Does this convey urgency?';

type Snippet = Pick<ModelEntry, 'id' | 'capabilities'>;

/** Only reachable through POST /v1/systemone. */
export const isSystemOneOnly = (e: Snippet): boolean =>
  e.capabilities.includes('systemone') && !e.capabilities.includes(WIRE_CHAT);

export function curlSnippet(e: Snippet, base: string): string {
  if (isSystemOneOnly(e)) {
    return `curl ${base}/v1/systemone \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${e.id}", "state": "${STATE}", "questions": {"is_urgent": {"type": "noul", "instructions": "${QUESTION}"}}}'`;
  }
  if (
    e.capabilities.includes('embeddings') &&
    !e.capabilities.includes(WIRE_CHAT)
  ) {
    return `curl ${base}/v1/embeddings \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${e.id}", "input": "hello"}'`;
  }
  return `curl ${base}/v1/chat/completions \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${e.id}", "messages": [{"role": "user", "content": "hello"}]}'`;
}

export function pythonSnippet(e: Snippet, base: string): string {
  if (isSystemOneOnly(e)) {
    return `# pip install typesafe-sdk
# export TYPESAFE_BASE_URL=${base}
# export TYPESAFE_API_KEY=YOUR_LURUS_API_KEY
from typesafe_sdk import Noul, TypeSafeClient

client = TypeSafeClient(model="${e.id}")
response = client.system_one(
    state="${STATE}",
    questions={"is_urgent": Noul(instructions="${QUESTION}")},
)
print(response.answers["is_urgent"].noul)`;
  }
  return `import requests

resp = requests.post(
    "${base}/v1/chat/completions",
    headers={"Authorization": "Bearer YOUR_LURUS_API_KEY"},
    json={"model": "${e.id}", "messages": [{"role": "user", "content": "hello"}]},
)
print(resp.json()["choices"][0]["message"]["content"])`;
}
