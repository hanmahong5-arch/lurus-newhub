import { readFileSync } from 'node:fs';
import { test as base, expect, type APIRequestContext } from '@playwright/test';
import {
  ACME_ADMIN_STATE,
  ADMIN_STATE,
  CUSTOMER_STATE,
  ENV,
  LEAD_STATE,
  WORLD_FILE,
} from './env';
import type { World } from './world.setup';

/**
 * The scenario test object.
 *
 *  - `admin`    the operator's session (root, tenant admin);
 *  - `customer` the linked customer's session (see world.setup.ts);
 *  - `acmeAdmin` the enterprise tenant's admin (its payer);
 *  - `lead`     the enterprise tenant's department lead (department A only);
 *  - `request`  Playwright's own, with no session — the relay is called with
 *               API keys only, as a customer's code calls it;
 *  - `world`    what setup created.
 *
 * Every scenario must:
 *  - name its case in the title ("TC-M1 …"), the id in
 *    doc/uat/business-acceptance-tests.md;
 *  - run at least one step titled "reverse control: …" — an input that must
 *    fail, checked to really fail, so a scenario that cannot fail does not
 *    pass. scripts/acceptance-meta-gate.mjs rejects a run where any scenario
 *    lacks one.
 */
export const test = base.extend<{
  admin: APIRequestContext;
  customer: APIRequestContext;
  acmeAdmin: APIRequestContext;
  lead: APIRequestContext;
  world: World;
}>({
  world: async ({}, use) => {
    await use(JSON.parse(readFileSync(WORLD_FILE, 'utf8')) as World);
  },
  admin: async ({ playwright }, use) => {
    const ctx = await playwright.request.newContext({
      baseURL: ENV.baseURL,
      storageState: ADMIN_STATE,
    });
    await use(ctx);
    await ctx.dispose();
  },
  customer: async ({ playwright }, use) => {
    const ctx = await playwright.request.newContext({
      baseURL: ENV.baseURL,
      storageState: CUSTOMER_STATE,
    });
    await use(ctx);
    await ctx.dispose();
  },
  acmeAdmin: async ({ playwright }, use) => {
    const ctx = await playwright.request.newContext({
      baseURL: ENV.baseURL,
      storageState: ACME_ADMIN_STATE,
    });
    await use(ctx);
    await ctx.dispose();
  },
  lead: async ({ playwright }, use) => {
    const ctx = await playwright.request.newContext({
      baseURL: ENV.baseURL,
      storageState: LEAD_STATE,
    });
    await use(ctx);
    await ctx.dispose();
  },
});

/** A step that proves the scenario can fail. */
export function reverseControl<T>(
  what: string,
  body: () => Promise<T>,
): Promise<T> {
  return test.step(`reverse control: ${what}`, body);
}

/** A per-run name, so a reused database never mixes two runs' rows. */
export function named(world: World, what: string): string {
  return `acc-${what}-${world.runId}`;
}

export { expect };
