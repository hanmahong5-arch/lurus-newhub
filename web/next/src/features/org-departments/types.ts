/** One project as projected by handler.projectView (department in the UI). */
export interface Department {
  id: number
  name: string
  description: string
  /** Internal quota units; 0 = no budget. */
  monthlyBudgetQuota: number
  externalCode: string
  deleted: boolean
}

/** Month-to-date consume spend per project id (internal quota units). */
export type SpendByProject = Record<number, number>

export interface SpendResult {
  byProject: SpendByProject
}

/** handler ProjectMemberUser. */
export interface DepartmentMember {
  userId: number
  username: string
  displayName: string
  tenantRole: string
  addedAt: number
}

export interface DepartmentWriteBody {
  name: string
  description: string
  monthly_budget_quota: number
  external_code: string
}
