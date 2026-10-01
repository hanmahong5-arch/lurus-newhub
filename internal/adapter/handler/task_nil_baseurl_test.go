package handler

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
)

// A music-task channel saved without a base_url (validateChannel allows it) used to
// nil-deref `*channel.BaseURL` inside the leader's 15s poller, which runs under
// a bare errgroup goroutine — one such channel with one unfinished task killed
// the whole process. The poll must degrade to a per-channel error instead.
func TestUpdateMusicTaskAll_NilBaseURLDoesNotPanic(t *testing.T) {
	app.InitHttpClient() // the poller runs after boot, which has built the shared client
	cleanup := setupContextTestDB(t)
	defer cleanup()

	ch := &repo.Channel{
		Type:   constant.ChannelTypeSunoAPI,
		Key:    "sk-test",
		Name:   "task-no-baseurl",
		Status: 1,
		Models: "model-a",
		Group:  "default",
	}
	if err := repo.DB.Create(ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	// The ORM default fills "" on insert; force the NULL a legacy/imported row has.
	if err := repo.DB.Exec("UPDATE channels SET base_url = NULL WHERE id = ?", ch.Id).Error; err != nil {
		t.Fatalf("null base_url: %v", err)
	}
	if got, err := repo.GetChannelById(ch.Id, true); err != nil || got.BaseURL != nil {
		t.Fatalf("precondition: want NULL base_url, got %v err=%v", got, err)
	}

	task := &repo.Task{TaskID: "task-a", Platform: constant.TaskPlatformSuno, ChannelId: ch.Id}
	taskM := map[string]*repo.Task{"task-a": task}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("updateSunoTaskAll panicked on nil BaseURL: %v\n%s", r, debug.Stack())
		}
	}()
	if err := updateSunoTaskAll(context.Background(), ch.Id, []string{"task-a"}, taskM); err == nil {
		t.Errorf("expected a fetch error for an empty base url, got nil")
	}
}
