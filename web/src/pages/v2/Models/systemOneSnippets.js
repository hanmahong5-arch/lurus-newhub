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

// Quick-start snippets for a System One model (POST /v1/systemone). The wire
// is TypeSafe's own, so the official SDK works against the hub unchanged once
// TYPESAFE_BASE_URL points at the hub origin.

const STATE = 'Help! My payouts have been failing for 3 days.';
const QUESTION = 'Does this convey urgency?';

// True when the model's only way in is /v1/systemone. Entries whose
// capabilities are unknown (empty) stay on the default chat snippet.
export const isSystemOneOnly = (e) =>
  e.capabilities.includes('systemone') && !e.capabilities.includes('openai');

export const systemOneCurl = (model, base) => `curl ${base}/v1/systemone \\
  -H "Authorization: Bearer $LURUS_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "${model}", "state": "${STATE}", "questions": {"is_urgent": {"type": "noul", "instructions": "${QUESTION}"}}}'`;

export const systemOnePython = (model, base) => `# pip install typesafe-sdk
# export TYPESAFE_BASE_URL=${base}
# export TYPESAFE_API_KEY=YOUR_LURUS_API_KEY
from typesafe_sdk import Noul, TypeSafeClient

client = TypeSafeClient(model="${model}")
response = client.system_one(
    state="${STATE}",
    questions={"is_urgent": Noul(instructions="${QUESTION}")},
)
print(response.answers["is_urgent"].noul)`;
