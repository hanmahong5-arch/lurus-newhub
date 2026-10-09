package governance

// Audit taxonomy for relay data control (migration 050): log retention,
// content rules and channel override templates. Kept in its own file so the
// shared audit_action.go only needs the validAuditActions entries.
const (
	ActionContentRetentionSet    = "data_policy.retention_set"
	ActionContentRuleCreated     = "data_policy.rule_created"
	ActionContentRuleUpdated     = "data_policy.rule_updated"
	ActionContentRuleDeleted     = "data_policy.rule_deleted"
	ActionContentRuleHit         = "data_policy.rule_hit"
	ActionChannelTemplateCreated = "channel_template.created"
	ActionChannelTemplateUpdated = "channel_template.updated"
	ActionChannelTemplateDeleted = "channel_template.deleted"
	ActionChannelTemplateApplied = "channel_template.applied"
	ResourceDataPolicy           = "data_policy"
	ResourceContentRule          = "content_rule"
	ResourceChannelTemplate      = "channel_template"
)
