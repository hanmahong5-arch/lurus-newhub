package entity

// cov_task_test.go — business-acceptance tests for Task's status mapping to
// the OpenAI-video wire vocabulary, the JSON blob accessors used to persist
// per-task opaque payloads, and the driver.Valuer/Scanner pair for the
// embedded JSON columns (Properties/TaskPrivateData), including the
// "empty struct marshals to SQL NULL not '{}'" business rule.

import (
	"encoding/json"
	"testing"
)

func TestTaskStatus_ToVideoStatus(t *testing.T) {
	tests := []struct {
		name   string
		status TaskStatus
		want   string
	}{
		{"queued", TaskStatusQueued, "queued"},
		{"submitted maps to queued too (submitted is a queued sub-state)", TaskStatusSubmitted, "queued"},
		{"in progress", TaskStatusInProgress, "in_progress"},
		{"success maps to completed", TaskStatusSuccess, "completed"},
		{"failure maps to failed", TaskStatusFailure, "failed"},
		{"explicit unknown status maps to unknown", TaskStatusUnknown, "unknown"},
		{"unrecognized/garbage status defaults to unknown, does not panic", TaskStatus("some-vendor-specific-status"), "unknown"},
		{"empty status defaults to unknown", TaskStatus(""), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.ToVideoStatus(); got != tt.want {
				t.Fatalf("ToVideoStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTask_SetData_GetData_RoundTrip(t *testing.T) {
	type payload struct {
		Foo string `json:"foo"`
		N   int    `json:"n"`
	}
	tsk := &Task{}
	tsk.SetData(payload{Foo: "bar", N: 5})

	var got payload
	if err := tsk.GetData(&got); err != nil {
		t.Fatalf("GetData() error: %v", err)
	}
	if got.Foo != "bar" || got.N != 5 {
		t.Fatalf("GetData() round trip mismatch: %#v", got)
	}
}

func TestTask_GetData_OnEmptyDataErrors(t *testing.T) {
	tsk := &Task{}
	var out map[string]any
	if err := tsk.GetData(&out); err == nil {
		t.Fatal("GetData() on never-set Data returned nil error, want unmarshal error for empty byte slice")
	}
}

func TestTask_ToOpenAIVideo(t *testing.T) {
	tsk := &Task{
		TaskID:     "task-123",
		Status:     TaskStatusSuccess,
		Progress:   "80%",
		CreatedAt:  1000,
		UpdatedAt:  2000,
		FailReason: "https://cdn.example.com/out.mp4",
		Properties: Properties{OriginModelName: "sora-2"},
	}
	video := tsk.ToOpenAIVideo()
	if video.ID != "task-123" {
		t.Fatalf("ID = %q, want task-123", video.ID)
	}
	if video.Status != "completed" {
		t.Fatalf("Status = %q, want completed (mapped from SUCCESS)", video.Status)
	}
	if video.Model != "sora-2" {
		t.Fatalf("Model = %q, want sora-2", video.Model)
	}
	if video.Progress != 80 {
		t.Fatalf("Progress = %d, want 80 (parsed from '80%%')", video.Progress)
	}
	if video.CreatedAt != 1000 || video.CompletedAt != 2000 {
		t.Fatalf("CreatedAt/CompletedAt = %d/%d, want 1000/2000", video.CreatedAt, video.CompletedAt)
	}
	if video.Metadata["url"] != "https://cdn.example.com/out.mp4" {
		t.Fatalf("Metadata[url] = %v, want the fail-reason-carried URL", video.Metadata["url"])
	}
}

func TestProperties_Value_Scan(t *testing.T) {
	t.Run("zero value marshals to SQL NULL, not empty-object json", func(t *testing.T) {
		v, err := Properties{}.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		if v != nil {
			t.Fatalf("Value() on zero Properties = %#v, want nil (SQL NULL)", v)
		}
	})

	t.Run("non-zero value marshals to its JSON encoding", func(t *testing.T) {
		p := Properties{Input: "hello", OriginModelName: "gpt-4"}
		v, err := p.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		b, ok := v.([]byte)
		if !ok {
			t.Fatalf("Value() returned %T, want []byte", v)
		}
		var roundTrip Properties
		if err := json.Unmarshal(b, &roundTrip); err != nil {
			t.Fatalf("re-unmarshal failed: %v", err)
		}
		if roundTrip != p {
			t.Fatalf("round trip mismatch: got %#v, want %#v", roundTrip, p)
		}
	})

	t.Run("Scan on empty bytes resets to zero value", func(t *testing.T) {
		m := &Properties{Input: "stale-data"}
		if err := m.Scan([]byte{}); err != nil {
			t.Fatalf("Scan([]byte{}) error: %v", err)
		}
		if *m != (Properties{}) {
			t.Fatalf("Scan(empty) = %#v, want zero value (stale data must be cleared)", m)
		}
	})

	t.Run("Scan on nil value resets to zero value", func(t *testing.T) {
		m := &Properties{Input: "stale-data"}
		if err := m.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) error: %v", err)
		}
		if *m != (Properties{}) {
			t.Fatalf("Scan(nil) = %#v, want zero value", m)
		}
	})

	t.Run("Scan decodes well-formed json bytes", func(t *testing.T) {
		var m Properties
		if err := m.Scan([]byte(`{"input":"x","origin_model_name":"claude-3"}`)); err != nil {
			t.Fatalf("Scan() error: %v", err)
		}
		if m.Input != "x" || m.OriginModelName != "claude-3" {
			t.Fatalf("Scan() decoded = %#v", m)
		}
	})

	t.Run("Scan on malformed json bytes returns an error", func(t *testing.T) {
		var m Properties
		if err := m.Scan([]byte(`{not-json`)); err == nil {
			t.Fatal("Scan() on malformed json returned nil error, want a decode error")
		}
	})
}

func TestTaskPrivateData_Value_Scan(t *testing.T) {
	t.Run("zero value marshals to SQL NULL", func(t *testing.T) {
		v, err := TaskPrivateData{}.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		if v != nil {
			t.Fatalf("Value() on zero TaskPrivateData = %#v, want nil", v)
		}
	})

	t.Run("non-zero value marshals its key", func(t *testing.T) {
		p := TaskPrivateData{Key: "secret-material"}
		v, err := p.Value()
		if err != nil {
			t.Fatalf("Value() error: %v", err)
		}
		b, ok := v.([]byte)
		if !ok {
			t.Fatalf("Value() returned %T, want []byte", v)
		}
		var roundTrip TaskPrivateData
		if err := json.Unmarshal(b, &roundTrip); err != nil {
			t.Fatalf("re-unmarshal failed: %v", err)
		}
		if roundTrip.Key != "secret-material" {
			t.Fatalf("round trip mismatch: %#v", roundTrip)
		}
	})

	t.Run("Scan on empty bytes leaves zero value untouched and returns no error", func(t *testing.T) {
		var p TaskPrivateData
		if err := p.Scan([]byte{}); err != nil {
			t.Fatalf("Scan([]byte{}) error: %v", err)
		}
		if p.Key != "" {
			t.Fatalf("Scan(empty) = %#v, want zero value", p)
		}
	})

	t.Run("Scan decodes well-formed json", func(t *testing.T) {
		var p TaskPrivateData
		if err := p.Scan([]byte(`{"key":"abc"}`)); err != nil {
			t.Fatalf("Scan() error: %v", err)
		}
		if p.Key != "abc" {
			t.Fatalf("Scan() = %#v, want key=abc", p)
		}
	})

	t.Run("Scan on malformed json returns an error", func(t *testing.T) {
		var p TaskPrivateData
		if err := p.Scan([]byte(`{broken`)); err == nil {
			t.Fatal("Scan() on malformed json returned nil error, want a decode error")
		}
	})
}
