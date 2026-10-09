package repo

import "testing"

func TestConvertLogToSearchLog_CarriesEmployeeRef(t *testing.T) {
	got := convertLogToSearchLog(&Log{Id: 3, EmployeeRef: "emp-7", ProjectId: 9})
	if got.EmployeeRef != "emp-7" || got.ProjectId != 9 {
		t.Fatalf("converted = %+v, want EmployeeRef emp-7 and ProjectId 9", got)
	}
}
