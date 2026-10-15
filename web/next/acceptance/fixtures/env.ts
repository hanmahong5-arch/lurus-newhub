/**
 * The instance under test and its fakes, from scripts/acceptance-stack.sh.
 * Missing values throw: these scenarios never skip.
 */

function required(name: string): string {
  const v = process.env[name];
  if (!v) {
    throw new Error(
      `${name} is not set — start the stack with scripts/acceptance-stack.sh`,
    );
  }
  return v;
}

export const ENV = {
  target: required('ACCEPT_TARGET'),
  baseURL: required('ACCEPT_BASE_URL'),
  bridgeToken: required('ACCEPT_BRIDGE_TOKEN'),
  fakeURL: required('ACCEPT_FAKE_URL'),
  vendorKey: process.env.ACCEPT_VENDOR_KEY || '',
  // Direct database access, for the one thing the API cannot do: stamp a
  // customer admin's tenant_role (see fixtures/enterprise.ts). Optional here,
  // required (and failing loudly) where it is used.
  sqlDSN: process.env.ACCEPT_SQL_DSN || '',
  pgContainer: process.env.ACCEPT_PG_CONTAINER || '',
};

/** The seeded default tenant: id `default`, routable slug `lurus`. */
export const TENANT_ID = 'default';
export const TENANT_SLUG = 'lurus';

/** The root user every fresh instance creates at boot. */
export const ROOT_USER_ID = 1;

export const STATE_DIR = 'acceptance-report/.state';
export const WORLD_FILE = `${STATE_DIR}/world.json`;
export const ADMIN_STATE = `${STATE_DIR}/admin.json`;
export const CUSTOMER_STATE = `${STATE_DIR}/customer.json`;
/** The enterprise tenant's admin (its payer) and its department lead. */
export const ACME_ADMIN_STATE = `${STATE_DIR}/acme-admin.json`;
export const LEAD_STATE = `${STATE_DIR}/dept-lead.json`;

/** Tenant-scoped v2 path: v2('/tokens') -> /api/v2/lurus/tokens. */
export function v2(path: string): string {
  return `/api/v2/${TENANT_SLUG}${path}`;
}
