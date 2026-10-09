import type { Department, DepartmentMember, SpendResult } from '../types'

function num(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {}
}

/** GET /projects -> `{items}`; deleted rows never reach the page. */
export function mapDepartments(raw: unknown): Department[] {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items
    .map((p): Department => {
      const r = record(p)
      return {
        id: num(r.id),
        name: str(r.name),
        description: str(r.description),
        monthlyBudgetQuota: num(r.monthly_budget_quota),
        externalCode: str(r.external_code),
        deleted: r.deleted === true,
      }
    })
    .filter((d) => d.id > 0 && !d.deleted)
}

/** GET /projects/spend -> `{items:[{project_id,total_quota,unassigned}]}`. */
export function mapSpend(raw: unknown): SpendResult {
  const items = record(raw).items
  const byProject: Record<number, number> = {}
  if (Array.isArray(items)) {
    for (const row of items) {
      const r = record(row)
      const id = num(r.project_id)
      if (id > 0 && r.unassigned !== true) {
        byProject[id] = (byProject[id] ?? 0) + num(r.total_quota)
      }
    }
  }
  return { byProject }
}

/** GET /projects/:id/members -> `{items}`. */
export function mapMembers(raw: unknown): DepartmentMember[] {
  const items = record(raw).items
  if (!Array.isArray(items)) return []
  return items.map((m): DepartmentMember => {
    const r = record(m)
    return {
      userId: num(r.user_id),
      username: str(r.username),
      displayName: str(r.display_name),
      tenantRole: str(r.tenant_role),
      addedAt: num(r.added_at),
    }
  })
}

/**
 * What the signed-in person gets to see. A tenant admin sees every
 * department. A department lead gets only the departments the spend endpoint
 * returns for them (the server filters that report to their own projects; the
 * project list itself is tenant-wide and must not be shown as "mine").
 */
export function visibleDepartments(
  all: Department[],
  spend: SpendResult | undefined,
  isAdmin: boolean
): Department[] {
  if (isAdmin) return all
  if (spend === undefined) return []
  return all.filter((d) => d.id in spend.byProject)
}
