package governance

// Audit taxonomy for the opt-in body archive (migration 052): the tenant's
// consent toggle and every read of an archived prompt/response. Kept in its
// own file, like audit_action_data_policy.go, so audit_action.go only needs
// the validAuditActions entries.
const (
	ActionSedimentationConsentSet = "data_policy.sedimentation_consent_set"
	ActionLogBodyRead             = "log_body.read"
	ResourceLogBody               = "log_body"
)
