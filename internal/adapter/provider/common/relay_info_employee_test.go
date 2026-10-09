package common

import (
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// The employee attribution (migration 045) must be copied from the gin
// context into RelayInfo for the same reason ProjectId is: settlement has no
// gin.Context.
func TestGenBaseRelayInfo_CarriesEmployeeRef(t *testing.T) {
	c := newRelayInfoCtx()
	common.SetContextKey(c, constant.ContextKeyEmployeeRef, "emp-31")
	if got := genBaseRelayInfo(c, nil).EmployeeRef; got != "emp-31" {
		t.Errorf("RelayInfo.EmployeeRef = %q, want emp-31", got)
	}
	if got := genBaseRelayInfo(newRelayInfoCtx(), nil).EmployeeRef; got != "" {
		t.Errorf("RelayInfo.EmployeeRef = %q, want empty with no context value", got)
	}
}
