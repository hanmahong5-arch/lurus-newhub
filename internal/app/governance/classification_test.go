package governance

import "testing"

func TestIsExportSafe_PublicFields(t *testing.T) {
	publicFields := []string{"model_name", "channel_type", "relay_mode", "total_latency_ms", "quota", "prompt_tokens", "completion_tokens", "request_id", "session_id"}
	for _, f := range publicFields {
		if !IsExportSafe(f) {
			t.Errorf("expected field %q to be export-safe (Public tier)", f)
		}
	}
}

func TestIsExportSafe_InternalFields(t *testing.T) {
	internalFields := []string{"channel_id", "model_ratio", "group_ratio", "model_price", "admin_info", "frt", "data_flow_source"}
	for _, f := range internalFields {
		if !IsExportSafe(f) {
			t.Errorf("expected field %q to be export-safe (Internal tier, admin-visible)", f)
		}
	}
}

func TestIsExportSafe_ConfidentialFields(t *testing.T) {
	confidentialFields := []string{"user_id", "username", "token_id", "token_name", "ip", "client_ip", "tenant_id", "end_user"}
	for _, f := range confidentialFields {
		if IsExportSafe(f) {
			t.Errorf("expected field %q to NOT be export-safe (Confidential tier)", f)
		}
	}
}

func TestIsExportSafe_UnknownFields(t *testing.T) {
	if IsExportSafe("some_unknown_field_xyz") {
		t.Error("unknown fields should default to NOT export-safe")
	}
}

func TestFieldClassification_AllTiersPresent(t *testing.T) {
	tiers := map[DataTier]bool{}
	for _, tier := range FieldClassification {
		tiers[tier] = true
	}
	if !tiers[TierPublic] {
		t.Error("no Public tier fields in classification")
	}
	if !tiers[TierInternal] {
		t.Error("no Internal tier fields in classification")
	}
	if !tiers[TierConfidential] {
		t.Error("no Confidential tier fields in classification")
	}
	// TierRestricted fields should NOT appear in the map (they are never stored).
}

func TestDataTier_Ordering(t *testing.T) {
	if TierPublic >= TierInternal {
		t.Error("TierPublic should be less than TierInternal")
	}
	if TierInternal >= TierConfidential {
		t.Error("TierInternal should be less than TierConfidential")
	}
	if TierConfidential >= TierRestricted {
		t.Error("TierConfidential should be less than TierRestricted")
	}
}
