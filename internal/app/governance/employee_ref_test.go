package governance

import (
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// EnrichLogParams is the single chokepoint every consume log passes through,
// so the employee attribution (migration 045) must be stamped here: from
// RelayInfo first (settlement has no gin.Context), context as the fallback.
func TestEnrichLogParams_EmployeeRefSources(t *testing.T) {
	t.Run("relay info wins", func(t *testing.T) {
		c := newTestContext()
		common.SetContextKey(c, constant.ContextKeyEmployeeRef, "from-ctx")
		params := &entity.RecordConsumeLogParams{}
		EnrichLogParams(c, &relaycommon.RelayInfo{EmployeeRef: "from-info", StartTime: time.Now()}, params)
		if params.EmployeeRef != "from-info" {
			t.Errorf("EmployeeRef = %q, want from-info", params.EmployeeRef)
		}
	})
	t.Run("context fallback", func(t *testing.T) {
		c := newTestContext()
		common.SetContextKey(c, constant.ContextKeyEmployeeRef, "from-ctx")
		params := &entity.RecordConsumeLogParams{}
		EnrichLogParams(c, &relaycommon.RelayInfo{StartTime: time.Now()}, params)
		if params.EmployeeRef != "from-ctx" {
			t.Errorf("EmployeeRef = %q, want from-ctx", params.EmployeeRef)
		}
	})
	t.Run("unattributed stays empty", func(t *testing.T) {
		params := &entity.RecordConsumeLogParams{}
		EnrichLogParams(newTestContext(), &relaycommon.RelayInfo{StartTime: time.Now()}, params)
		if params.EmployeeRef != "" {
			t.Errorf("EmployeeRef = %q, want empty", params.EmployeeRef)
		}
	})
}
