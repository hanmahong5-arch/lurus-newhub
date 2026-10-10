package governance

// Audit taxonomy for decision-model routing (migration 054): the per-request
// routing decision and tenant-admin edits of a routing policy. Kept in its own
// file, like audit_action_data_policy.go. The allow-set registration lives in
// init() here so this lane does not have to edit the shared audit_action.go;
// TestEveryActionConstantIsInTheAllowSet still proves every constant below is
// accepted by IsValidAuditAction.
const (
	// ActionRoutingDecision is written once per request that matched an enabled
	// policy. Details carry the outcome, the chosen model, confidence, the
	// probability distribution and the evaluator usage - never the user text.
	ActionRoutingDecision      = "routing.decision"
	ActionRoutingPolicySet     = "routing.policy_set"
	ActionRoutingPolicyDeleted = "routing.policy_deleted"
	ResourceRoutingPolicy      = "routing_policy"
)

func init() {
	for _, a := range []string{ActionRoutingDecision, ActionRoutingPolicySet, ActionRoutingPolicyDeleted} {
		validAuditActions[a] = struct{}{}
	}
}
