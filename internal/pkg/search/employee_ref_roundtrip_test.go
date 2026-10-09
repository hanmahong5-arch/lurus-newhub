package search

import "testing"

// EmployeeRef must survive Log -> document -> Log; a missing hop silently
// empties per-employee search hits.
func TestEmployeeRef_RoundTrip(t *testing.T) {
	doc := ConvertLogToDocument(&Log{Id: 1, EmployeeRef: "emp-42"})
	if doc.EmployeeRef != "emp-42" {
		t.Fatalf("document EmployeeRef = %q, want emp-42", doc.EmployeeRef)
	}
	if back := ConvertDocumentToLog(doc); back.EmployeeRef != "emp-42" {
		t.Fatalf("round-tripped EmployeeRef = %q, want emp-42", back.EmployeeRef)
	}
}
