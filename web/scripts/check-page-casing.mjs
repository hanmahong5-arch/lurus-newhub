#!/usr/bin/env node
// check-page-casing.mjs
// Static casing scanner — guards against camelCase reads of snake_case backend
// JSON fields in v2 pages. Exits non-zero if any violation is found.
//
// Run: node web/scripts/check-page-casing.mjs
// or:  bun run --cwd web scripts/check-page-casing.mjs

import { readFileSync, readdirSync, statSync } from 'fs';
import { join, relative } from 'path';
import { fileURLToPath } from 'url';
import { dirname } from 'path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const WEB_ROOT = join(__dirname, '..');
const PAGES_ROOT = join(WEB_ROOT, 'src', 'pages', 'v2');

// Snake_case-violating camelCase property accesses derived from backend JSON
// tags across repo.Tenant, repo.Token, repo.Channel, repo.Log,
// repo.TenantStats, and repo.User entities. Extend this list when new
// entities are added. Each entry is a regex-safe property access pattern.
const VIOLATIONS = [
  // Tenant entity (internal/domain/entity/tenant.go)
  '\\.maxUsers\\b',
  '\\.planType\\b',
  '\\.maxQuota\\b',
  // wire 物理列名 zitadel_org_id (待 idp-migration); guards camelCase reads of
  // the still-live snake_case JSON key — keep until the backend column renames.
  '\\.zitadelOrgId\\b',
  '\\.zitadelOrgID\\b',

  // TenantStats
  '\\.userCount\\b',
  '\\.tokenCount\\b',
  '\\.channelCount\\b',
  '\\.totalQuotaUsed\\b',
  '\\.totalQuotaRemaining\\b',
  '\\.activeSubscriptions\\b',

  // Token entity (repo/token.go)
  '\\.accessToken\\b',
  '\\.refreshToken\\b',
  '\\.expiredTime\\b',
  '\\.accessedTime\\b',
  '\\.remainQuota\\b',
  '\\.usedQuota\\b',
  '\\.unlimitedQuota\\b',
  '\\.modelLimits\\b',

  // Log entity
  '\\.createdAt\\b',
  '\\.updatedAt\\b',
  '\\.modelName\\b',
  '\\.channelName\\b',
  '\\.totalLatencyMs\\b',

  // User entity (previously PascalCase reads from commit 871e49c1 era)
  '\\.requestCount\\b',
  '\\.tokenCount\\b',
  '\\.remainingQuota\\b',
  '\\.usedQuota\\b',
  '\\.displayName\\b', // only when reading from API response; component state OK
];

// Allowlist: file-level patterns that are exempt from the check.
// Add entries as "filepath:pattern" if a false-positive arises.
const ALLOWLIST = new Set([
  // kpis.js: `requestCount` is a local computed aggregation key in
  // computeCostByModel — NOT a direct backend JSON field read. The function
  // builds its own derived objects { model, totalQuota, requestCount } from
  // snake_case log entries. Allowlisted as internal-state camelCase.
  'Dashboard/kpis.js',
  // Dashboard/index.jsx line 530: reads `row.requestCount` from the
  // computeCostByModel() derived object (see above) — not a backend key.
  'Dashboard/index.jsx',
  // Test files are exempt — they may reference keys to assert violations.
  'index.test.jsx',
  'CreditPoolDrawer.test.jsx',
  'static-pages.test.jsx',
]);

function isAllowlisted(relPath) {
  // Normalize backslashes to forward slashes for cross-platform matching.
  const normalized = relPath.replace(/\\/g, '/');
  for (const pat of ALLOWLIST) {
    if (normalized.includes(pat)) return true;
  }
  return false;
}

function walkJsx(dir, files = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      walkJsx(full, files);
    } else if (entry.endsWith('.jsx') || entry.endsWith('.js')) {
      files.push(full);
    }
  }
  return files;
}

const violationRegexes = VIOLATIONS.map((p) => new RegExp(p, 'g'));

let totalViolations = 0;
const report = [];

for (const filePath of walkJsx(PAGES_ROOT)) {
  const relPath = relative(PAGES_ROOT, filePath);
  if (isAllowlisted(relPath)) continue;

  const src = readFileSync(filePath, 'utf8');
  const lines = src.split('\n');

  for (const [lineIdx, line] of lines.entries()) {
    for (const re of violationRegexes) {
      re.lastIndex = 0;
      let m;
      while ((m = re.exec(line)) !== null) {
        const lineNo = lineIdx + 1;
        report.push(`${relPath}:${lineNo}  ${line.trim()}`);
        totalViolations++;
      }
    }
  }
}

if (totalViolations === 0) {
  console.log('check-page-casing: OK — no snake_case violations found');
  process.exit(0);
} else {
  console.error(
    `check-page-casing: FAIL — ${totalViolations} camelCase violation(s) found in v2 pages:\n`,
  );
  for (const line of report) {
    console.error('  ', line);
  }
  console.error(
    '\nThese field accesses use camelCase but the backend sends snake_case JSON.',
    '\nFix the read paths or add a justified ALLOWLIST entry in check-page-casing.mjs.',
  );
  process.exit(1);
}
