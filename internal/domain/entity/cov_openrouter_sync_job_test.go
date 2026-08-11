package entity

// cov_openrouter_sync_job_test.go — business-acceptance tests for the
// OpenRouter sync-job scheduler decision (ShouldRun) and its Categories
// JSON accessor pair. ShouldRun is the sole gate that decides whether an
// automated model-import job fires; a bug here either spams upstream or
// silently stops syncing free models forever.

import (
	"testing"
	"time"
)

func TestOpenRouterSyncJob_Categories_RoundTrip(t *testing.T) {
	t.Run("empty column returns empty slice", func(t *testing.T) {
		j := &OpenRouterSyncJob{}
		if got := j.GetCategories(); len(got) != 0 {
			t.Fatalf("GetCategories() = %#v, want empty", got)
		}
	})

	t.Run("whitespace-only column returns empty slice", func(t *testing.T) {
		j := &OpenRouterSyncJob{Categories: "   "}
		if got := j.GetCategories(); len(got) != 0 {
			t.Fatalf("GetCategories() = %#v, want empty", got)
		}
	})

	t.Run("malformed json returns empty slice, not a panic", func(t *testing.T) {
		j := &OpenRouterSyncJob{Categories: "[not-json"}
		if got := j.GetCategories(); len(got) != 0 {
			t.Fatalf("GetCategories() = %#v, want empty", got)
		}
	})

	t.Run("set then get round trips exactly", func(t *testing.T) {
		j := &OpenRouterSyncJob{}
		cats := []string{OpenRouterCategoryVision, OpenRouterCategoryASR}
		if err := j.SetCategories(cats); err != nil {
			t.Fatalf("SetCategories() error: %v", err)
		}
		got := j.GetCategories()
		if len(got) != 2 || got[0] != OpenRouterCategoryVision || got[1] != OpenRouterCategoryASR {
			t.Fatalf("round trip mismatch: %#v", got)
		}
	})
}

func TestOpenRouterSyncJob_ShouldRun(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	hourAgo := now.Add(-1 * time.Hour)
	dayAgo := now.Add(-24 * time.Hour)
	dayAgoPlus := now.Add(-24*time.Hour - time.Second)
	weekAgo := now.Add(-7 * 24 * time.Hour)
	weekAgoPlus := now.Add(-7*24*time.Hour - time.Second)

	tests := []struct {
		name string
		job  OpenRouterSyncJob
		want bool
	}{
		{"disabled job never runs regardless of schedule", OpenRouterSyncJob{Enabled: false, Schedule: OpenRouterScheduleDaily}, false},
		{"manual schedule never auto-runs even if enabled", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleManual}, false},
		{"unknown schedule string defaults to never auto-run", OpenRouterSyncJob{Enabled: true, Schedule: "yearly"}, false},
		{"daily job never run before (LastRunAt nil) is due immediately", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: nil}, true},
		{"daily job run 1 hour ago is not yet due", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: &hourAgo}, false},
		{"daily job run exactly 24h ago is due (boundary, >= not >)", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: &dayAgo}, true},
		{"daily job run 24h+1s ago is due", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleDaily, LastRunAt: &dayAgoPlus}, true},
		{"weekly job never run before is due immediately", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: nil}, true},
		{"weekly job run 1 day ago is not yet due", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: &dayAgo}, false},
		{"weekly job run exactly 7 days ago is due (boundary)", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: &weekAgo}, true},
		{"weekly job run 7 days + 1s ago is due", OpenRouterSyncJob{Enabled: true, Schedule: OpenRouterScheduleWeekly, LastRunAt: &weekAgoPlus}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.job.ShouldRun(now); got != tt.want {
				t.Fatalf("ShouldRun() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOpenRouterSyncJob_TableName(t *testing.T) {
	if got := (OpenRouterSyncJob{}).TableName(); got != "openrouter_sync_jobs" {
		t.Fatalf("TableName() = %q, want openrouter_sync_jobs", got)
	}
}
