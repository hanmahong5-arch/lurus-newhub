#!/usr/bin/env node
// scripts/check-contract.mjs
// OpenAPI ↔ frontend fixture field alignment gate.
//
// Usage: node scripts/check-contract.mjs
// Exit 0 = clean. Exit 1 = mismatches found.
//
// Heuristic scanner: parses the components.schemas block from api-v2.yaml and
// extracts field names from test fixture objects. False positives may be
// suppressed with an inline comment:  // contract-allow:<reason>
//
// The scanner respects `// contract-allow:<reason>` on the same line or
// directly above the flagged field.

import { readFileSync, readdirSync, statSync } from 'fs';
import { join, resolve } from 'path';
import { fileURLToPath } from 'url';

// ─── Paths ────────────────────────────────────────────────────────────────────

const __dirname = fileURLToPath(new URL('.', import.meta.url));
const ROOT = resolve(__dirname, '..');
const OPENAPI_YAML = join(ROOT, 'docs', 'openapi', 'api-v2.yaml');
const TEST_GLOB_DIR = join(ROOT, 'web', 'src', 'pages', 'v2');

// ─── OpenAPI schema parser ────────────────────────────────────────────────────
// Parses components.schemas from the YAML text (handles CRLF line endings).
// Returns Map<schemaName, Set<fieldName>>.

function parseOpenAPISchemas(yamlText) {
  const schemas = new Map();
  // Normalise CRLF → LF
  const text = yamlText.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
  const lines = text.split('\n');

  let inComponents = false;
  let inSchemas = false;
  let currentSchema = null;
  let inProperties = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // Detect components: section (indent 0)
    if (/^components:/.test(line)) {
      inComponents = true;
      inSchemas = false;
      currentSchema = null;
      continue;
    }

    if (!inComponents) continue;

    // Detect leaving components: section (another top-level key)
    if (/^[a-zA-Z]/.test(line) && !/^components/.test(line)) {
      inComponents = false;
      inSchemas = false;
      continue;
    }

    // Detect schemas: (indent 2)
    if (/^  schemas:/.test(line)) {
      inSchemas = true;
      currentSchema = null;
      inProperties = false;
      continue;
    }

    // Detect leaving schemas: (another indent-2 key)
    if (inSchemas && /^  [a-zA-Z]/.test(line) && !/^  schemas/.test(line)) {
      inSchemas = false;
      currentSchema = null;
      inProperties = false;
      continue;
    }

    if (!inSchemas) continue;

    // Schema name (indent 4, PascalCase or camelCase)
    const schemaMatch = line.match(/^    ([A-Za-z][a-zA-Z0-9]+):\s*$/);
    if (schemaMatch) {
      currentSchema = schemaMatch[1];
      if (!schemas.has(currentSchema)) schemas.set(currentSchema, new Set());
      inProperties = false;
      continue;
    }

    // properties: keyword (indent 6)
    if (currentSchema && /^      properties:/.test(line)) {
      inProperties = true;
      continue;
    }

    // Leaving properties — detected by encountering another indent-6 key
    if (inProperties && /^      [a-zA-Z]/.test(line) && !/^        /.test(line)) {
      // Could be another keyword like 'required:', 'allOf:', etc.
      if (!/^      properties:/.test(line)) {
        inProperties = false;
      }
      continue;
    }

    // Property field (indent 8, snake_case or camelCase identifier followed by colon)
    if (inProperties && currentSchema) {
      const fieldMatch = line.match(/^        ([a-z][a-zA-Z0-9_]+):\s*/);
      if (fieldMatch) {
        schemas.get(currentSchema).add(fieldMatch[1]);
      }
    }
  }

  return schemas;
}

// ─── Test file scanner ────────────────────────────────────────────────────────

function findTestFiles(dir) {
  const results = [];
  try {
    const entries = readdirSync(dir);
    for (const entry of entries) {
      const full = join(dir, entry);
      const stat = statSync(full);
      if (stat.isDirectory()) {
        results.push(...findTestFiles(full));
      } else if (entry.endsWith('.test.jsx') || entry.endsWith('.test.tsx')) {
        results.push(full);
      }
    }
  } catch {
    // ignore unreadable dirs
  }
  return results;
}

// Extract field names from fixture objects in test files.
// Only looks at lines inside data: { ... } response fixtures, not mock setup.
// Returns Array<{file, line, field, allowlisted}>
function extractFixtureFields(filePath) {
  const text = readFileSync(filePath, 'utf-8');
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const fields = [];

  // Skip JS non-fixture identifiers
  const SKIP_NAMES = new Set([
    'get', 'post', 'put', 'delete', 'patch',
    'return', 'const', 'let', 'var', 'if', 'for', 'while', 'switch',
    'case', 'default', 'break', 'continue', 'import', 'export',
    'function', 'class', 'type', 'interface', 'describe', 'it',
    'expect', 'data', 'success', 'items', 'total', 'page', 'page_size',
    'message', 'error', 'url', 'href', 'target', 'value', 'headers',
    'method', 'body', 'status', 'success', 'onClick', 'onChange',
    'onConfirm', 'onCancel', 'onClose', 'visible', 'title', 'children',
    'actions', 'configurable', 'showError', 'showSuccess',
    'response', 'reason', 'direction',
  ]);

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // Only look at lines with 4+ spaces of indent (fixture object fields)
    // and identifier: value pattern
    const fieldMatch = line.match(/^(\s{4,})([a-z][a-zA-Z0-9_]*):\s*[^:]/);
    if (!fieldMatch) continue;

    const field = fieldMatch[2];
    if (SKIP_NAMES.has(field)) continue;

    // Skip if looks like a JS method or declaration
    if (line.trim().startsWith('//')) continue;
    if (line.includes('=>')) continue;
    if (line.includes('function')) continue;

    // Check allowlist annotation on same line or previous line
    const allowlisted =
      line.includes('// contract-allow:') ||
      (i > 0 && lines[i - 1].includes('// contract-allow:'));

    fields.push({
      file: filePath,
      line: i + 1,
      field,
      allowlisted,
      lineText: line.trim(),
    });
  }

  return fields;
}

// ─── Main ─────────────────────────────────────────────────────────────────────

// Fields that appear in fixtures but belong to non-v2-spec endpoints (internal,
// cluster, models custom, etc.) or are test infrastructure patterns.
// These are globally allowlisted. Inline `// contract-allow:<reason>` also works.
const GLOBAL_ALLOWLIST = new Set([
  // Pagination / response envelope sub-keys
  'channels', 'logs', 'tenants', 'bucket', 'limit', 'offset',
  // Cluster log endpoint (not yet in v2 spec — deferred endpoint)
  'model_name', 'error_code', 'count',
  // Models custom endpoint sub-fields (internal, not in v2 spec)
  'vendor', 'vendor_counts', 'status',
  // Billing invoice sub-fields (billing/invoices internal endpoint)
  'month', 'amount_cny', 'amount_usd', 'request_count',
  // Billing summary sub-fields (user/billing/summary internal endpoint)
  'wallet_balance_cny', 'mtd_spend_cny',
  'balance', 'frozen', 'available', 'lifetime_topup', 'lifetime_spend',
  'active_pre_auths', 'pending_orders',
  // Sessions endpoint (internal, not in v2 spec)
  'auth_method', 'active_tokens', 'last_seen', 'current',
  // Tenant fixture extra fields not yet in OpenAPI spec
  'plan_type', 'max_users', 'max_quota', 'updated_at', 'user_count',
  // Channel test response sub-fields
  'latency_ms', 'upstream', 'new', 'missing',
  // Checkout URL response field (billing checkout internal endpoint)
  'checkout_url',
  // Billing topup fields (already in TopUpList schema inline but heuristic misses)
  'amount',
  // Settings billing topup log fields
  'created_at', 'content',
  // Redemption bulk-create response
  'codes', 'requested',
  // Pricing endpoint (internal, not in v2 spec)
  'quota_type', 'model_ratio', 'completion_ratio', 'model_price',
  'enable_groups', 'supported_endpoint_types',
  // CreditPool drawer fields
  'pool_quota', 'pool_used', 'transactions',
  // Playground internal fields (relay error envelope, preset storage)
  'error_message', 'prompt', 'params',
  // Pricing endpoint sub-fields and response envelope (internal, not in v2 spec)
  'pricing', 'group_ratio',
  // Tenants create POST body field name diff (spec uses TenantCreate: slug+name+zitadel_org_id)
  'plan', 'quota_limit',
  // Common fields in multiple schemas
  'quota', 'used_quota', 'expired_time', 'full_key', 'slug',
]);

function run() {
  const yamlText = readFileSync(OPENAPI_YAML, 'utf-8');
  const schemas = parseOpenAPISchemas(yamlText);

  console.log(`Loaded ${schemas.size} OpenAPI schemas:`);
  for (const [name, fields] of schemas) {
    console.log(`  ${name}: [${[...fields].join(', ')}]`);
  }

  if (schemas.size === 0) {
    console.error('\nERROR: No schemas found — check OPENAPI_YAML path and YAML structure.');
    process.exit(1);
  }

  // Union of all known schema fields for heuristic matching
  const allKnownFields = new Set();
  for (const fields of schemas.values()) {
    for (const f of fields) allKnownFields.add(f);
  }

  const testFiles = findTestFiles(TEST_GLOB_DIR);
  console.log(`\nScanning ${testFiles.length} test files in ${TEST_GLOB_DIR}\n`);

  let mismatches = 0;
  let allowlistedCount = 0;

  for (const filePath of testFiles) {
    const fields = extractFixtureFields(filePath);
    const relative = filePath.replace(ROOT + '\\', '').replace(ROOT + '/', '');

    for (const { line, field, allowlisted, lineText } of fields) {
      if (GLOBAL_ALLOWLIST.has(field)) continue;
      if (allowlisted) {
        allowlistedCount++;
        continue;
      }
      if (allKnownFields.has(field)) continue;

      console.log(
        `MISMATCH ${relative}:${line} field \`${field}\` not in any OpenAPI schema`,
      );
      console.log(`  line: ${lineText}`);
      console.log(`  hint: add // contract-allow:<reason> to suppress\n`);
      mismatches++;
    }
  }

  console.log(`\n─── Summary ───────────────────────────────────────────`);
  console.log(`  Schemas parsed: ${schemas.size}`);
  console.log(`  Test files scanned: ${testFiles.length}`);
  console.log(`  Known fields (union): ${allKnownFields.size}`);
  console.log(`  Globally allowlisted: ${GLOBAL_ALLOWLIST.size}`);
  console.log(`  Inline allowlisted: ${allowlistedCount}`);
  console.log(`  Mismatches: ${mismatches}`);

  if (mismatches > 0) {
    console.log('\nContract gate FAILED — fix mismatches or add contract-allow annotations.');
    process.exit(1);
  }

  console.log('\nContract gate PASSED.');
  process.exit(0);
}

run();
