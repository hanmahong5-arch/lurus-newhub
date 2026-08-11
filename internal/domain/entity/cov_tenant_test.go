package entity

// cov_tenant_test.go — business-acceptance tests for Tenant status
// predicates. IsEnabled/IsDisabled gate tenant-wide access at the relay and
// admin layers; a wrong classification of TenantStatusSuspended would let a
// suspended (e.g. non-paying) tenant keep relaying.

import "testing"

func TestTenant_IsEnabled(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{"enabled", TenantStatusEnabled, true},
		{"disabled", TenantStatusDisabled, false},
		{"suspended", TenantStatusSuspended, false},
		{"zero value (unset column) is not enabled", 0, false},
		{"unknown status code is not enabled", 999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant := &Tenant{Status: tt.status}
			if got := tenant.IsEnabled(); got != tt.want {
				t.Fatalf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTenant_IsDisabled(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{"enabled is not disabled", TenantStatusEnabled, false},
		{"disabled", TenantStatusDisabled, true},
		{"suspended counts as disabled too (both block relay)", TenantStatusSuspended, true},
		{"unknown status code is not disabled (fail toward enabled, not silently locked out)", 999, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenant := &Tenant{Status: tt.status}
			if got := tenant.IsDisabled(); got != tt.want {
				t.Fatalf("IsDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTenant_TableName(t *testing.T) {
	if got := (Tenant{}).TableName(); got != "tenants" {
		t.Fatalf("TableName() = %q, want %q", got, "tenants")
	}
}
