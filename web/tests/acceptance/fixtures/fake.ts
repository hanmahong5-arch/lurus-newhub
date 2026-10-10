import { request as pwRequest, type APIRequestContext } from '@playwright/test';
import { ENV } from './env';
import { body } from './provision';

/**
 * The control side of cmd/fakeupstream: the fake vendor (/_fake/*) and the
 * fake platform wallet (/_fake/platform/*). These routes exist only on the
 * fake; nothing here touches the instance under test.
 */

let ctx: APIRequestContext | undefined;
async function fake(): Promise<APIRequestContext> {
  ctx ??= await pwRequest.newContext({ baseURL: ENV.fakeURL });
  return ctx;
}

export interface FakeRecord {
  method: string;
  path: string;
  wire: string;
  model: string;
  stream: boolean;
  fault?: string;
  status: number;
}

export async function vendorRequests(): Promise<FakeRecord[]> {
  const b = await body(
    await (await fake()).get('/_fake/requests'),
    'fake requests',
  );
  return b.requests ?? [];
}

/** Changes the usage the fake vendor reports from the next answer on. */
export async function setVendorUsage(u: {
  prompt_tokens: number;
  completion_tokens: number;
  cached_tokens?: number;
}): Promise<void> {
  await body(
    await (await fake()).post('/_fake/usage', { data: u }),
    'set fake usage',
  );
}

/** Makes the next `count` vendor calls answer with fault `mode`. */
export async function queueFault(mode: string, count = 1): Promise<void> {
  await body(
    await (await fake()).post('/_fake/faults', { data: { mode, count } }),
    'queue fault',
  );
}

export interface PlatformEntry {
  account_id: number;
  kind: 'preauth' | 'settle' | 'release' | 'debit' | 'credit';
  amount: number;
  preauth_id?: number;
}

export interface PlatformAccount {
  account: { id: number; balance: number; frozen: number };
  charged: number;
  ledger: PlatformEntry[] | null;
}

export async function addPlatformAccount(a: {
  id: number;
  idp_subject: string;
  email: string;
  balance: number;
}): Promise<void> {
  await body(
    await (await fake()).post('/_fake/platform/accounts', { data: a }),
    'add platform account',
  );
}

export async function platformAccount(id: number): Promise<PlatformAccount> {
  return body(
    await (await fake()).get(`/_fake/platform/accounts/${id}`),
    `platform account ${id}`,
  );
}

export async function platformUnhandled(): Promise<string[]> {
  const b = await body(
    await (await fake()).get('/_fake/platform/unhandled'),
    'platform unhandled',
  );
  return b.unhandled ?? [];
}
