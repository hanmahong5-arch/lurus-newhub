package common

import "testing"

func TestValidateChannelSetting_PlanMonthlyFee(t *testing.T) {
	for _, ok := range []string{``, `{}`, `{"plan_monthly_fee_cny4":0}`, `{"plan_monthly_fee_cny4":3000000}`} {
		if err := ValidateChannelSetting(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	if err := ValidateChannelSetting(`{"plan_monthly_fee_cny4":-1}`); err == nil {
		t.Error("negative plan fee must be rejected")
	}
	if err := ValidateChannelSetting(`{"plan_monthly_fee_cny4":"abc"}`); err == nil {
		t.Error("non-numeric plan fee must be rejected")
	}
}
