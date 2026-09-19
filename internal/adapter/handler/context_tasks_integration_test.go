package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/operation_setting"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"gorm.io/gorm"
)

// setupContextTestDB sets up an in-memory SQLite database for context task tests.
func setupContextTestDB(t *testing.T) func() {
	t.Helper()

	dbName := fmt.Sprintf("file:contexttest%d?mode=memory&cache=shared", testDBCounter.Add(1))
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	tables := []interface{}{
		&repo.User{},
		&repo.Channel{},
		&repo.Ability{},
		&repo.Task{},
		&repo.Midjourney{},
		&repo.Option{},
	}
	for _, tbl := range tables {
		if err := db.AutoMigrate(tbl); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				continue
			}
			t.Fatalf("auto migrate failed for %T: %v", tbl, err)
		}
	}

	// Save previous state
	prevDB := repo.DB
	prevLogDB := repo.LOG_DB
	prevSQLite := common.UsingSQLite
	prevPG := common.UsingPostgreSQL
	prevRedis := common.RedisEnabled
	prevMemCache := common.MemoryCacheEnabled

	repo.DB = db
	repo.LOG_DB = db
	repo.InitCol()
	common.UsingSQLite = true
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false

	// Initialize OptionMap
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()

	cleanup := func() {
		repo.DB = prevDB
		repo.LOG_DB = prevLogDB
		common.UsingSQLite = prevSQLite
		common.UsingPostgreSQL = prevPG
		common.RedisEnabled = prevRedis
		common.MemoryCacheEnabled = prevMemCache
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	return cleanup
}

// TestUpdateTaskBulkWithContext_WithEmptyTasks tests task update with no tasks in database.
func TestUpdateTaskBulkWithContext_WithEmptyTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		UpdateTaskBulkWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestUpdateTaskBulkWithContext_WithNullTaskID tests handling of tasks with empty TaskID.
func TestUpdateTaskBulkWithContext_WithNullTaskID(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create tasks with empty TaskID
	tasks := []*repo.Task{
		{
			TaskID:    "",
			Platform:  constant.TaskPlatformSuno,
			Status:    "PENDING",
			Progress:  "0%",
			ChannelId: 1,
		},
		{
			TaskID:    "",
			Platform:  constant.TaskPlatformSuno,
			Status:    "PENDING",
			Progress:  "0%",
			ChannelId: 1,
		},
	}
	for _, task := range tasks {
		if err := repo.DB.Create(task).Error; err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
	}

	// Use a very short timeout - the actual ticker won't fire, but context cancel will
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		UpdateTaskBulkWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK - context cancelled
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestUpdateMidjourneyTaskBulkWithContext_WithEmptyTasks tests midjourney task update with no tasks.
func TestUpdateMidjourneyTaskBulkWithContext_WithEmptyTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		UpdateMidjourneyTaskBulkWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestUpdateMidjourneyTaskBulkWithContext_WithPendingTasks tests midjourney task update with pending tasks.
func TestUpdateMidjourneyTaskBulkWithContext_WithPendingTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create a test channel first
	channel := &repo.Channel{
		Name:   "test-mj-channel",
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}

	// Create pending midjourney tasks
	tasks := []*repo.Midjourney{
		{
			MjId:      "mj-task-1",
			ChannelId: channel.Id,
			Status:    "PENDING",
			Progress:  "0%",
		},
	}
	for _, task := range tasks {
		if err := repo.DB.Create(task).Error; err != nil {
			t.Fatalf("failed to create midjourney task: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		UpdateMidjourneyTaskBulkWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestAutomaticallyUpdateChannelsWithContext_WithChannel tests channel update with existing channels.
func TestAutomaticallyUpdateChannelsWithContext_WithChannel(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create a test channel
	channel := &repo.Channel{
		Name:    "test-channel",
		Type:    1,
		Key:     "test-key",
		Status:  common.ChannelStatusEnabled,
		Balance: 100.0,
	}
	if err := repo.DB.Create(channel).Error; err != nil {
		t.Fatalf("failed to create channel: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		AutomaticallyUpdateChannelsWithContext(ctx, 60) // Long interval, won't fire
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestAutomaticallyTestChannelsWithContext_MasterEnabled tests channel testing when master and enabled.
func TestAutomaticallyTestChannelsWithContext_MasterEnabled(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Set up master node
	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		AutomaticallyTestChannelsWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestChannelHealthTest_SuccessfulTickStampsHeartbeat is the L3 heartbeat
// oracle for channel-health-test. As of cycle-11 L3 this job IS leader-gated
// (common.IsLeader() checked inside the tick case, taskreg.Register's
// leaderOnly=true) — the earlier "NOT leadership, spec forbids NewLeaderTask"
// note described the pre-cycle-11 shape, when every master-capable replica
// probed independently; TestChannelHealthTest_FollowerNeverLaunchesPass
// (channel_probe_auto_test.go) proves the follower side of the new gate,
// this test proves the leader side still stamps. NewLeaderTask itself is
// still deliberately NOT used (it would re-run a full ban-authority pass on
// every lease acquisition); the gate is a plain IsLeader() check instead,
// the same shape internal/lifecycle/audit_cleanup.go uses. Drives the real
// AutomaticallyTestChannelsWithContext ticker loop with AutoTestChannelMinutes
// forced to 0 so its first tick fires almost immediately, against an empty
// (real) channels table, and asserts
// metrics.LeaderTaskLastSuccess{task="channel-health-test"} advances.
func TestChannelHealthTest_SuccessfulTickStampsHeartbeat(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()

	prevLeader := common.IsLeader()
	common.SetLeader(true)
	defer func() { common.SetLeader(prevLeader) }()

	ms := operation_setting.GetMonitorSetting()
	prevEnabled, prevMinutes := ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes
	ms.AutoTestChannelEnabled = true
	ms.AutoTestChannelMinutes = 0 // rounds to a 0s wait — first tick fires immediately
	defer func() {
		ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes = prevEnabled, prevMinutes
	}()

	before := time.Now().Unix()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		AutomaticallyTestChannelsWithContext(ctx)
		close(done)
	}()
	<-done

	after := time.Now().Unix()
	got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues("channel-health-test"))
	if got < float64(before) || got > float64(after) {
		t.Errorf("LeaderTaskLastSuccess{task=channel-health-test} = %v, want within [%d, %d] (an empty channel table is a successful, trivial pass)", got, before, after)
	}
}

// TestMultipleFunctionsWithSameDB tests multiple context functions sharing same database state.
func TestMultipleFunctionsWithSameDB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create test data
	channel := &repo.Channel{
		Name:   "shared-channel",
		Type:   1,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}
	repo.DB.Create(channel)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var completed atomic.Int32
	const numFuncs = 3

	go func() {
		UpdateTaskBulkWithContext(ctx)
		completed.Add(1)
	}()

	go func() {
		UpdateMidjourneyTaskBulkWithContext(ctx)
		completed.Add(1)
	}()

	go func() {
		AutomaticallyUpdateChannelsWithContext(ctx, 60)
		completed.Add(1)
	}()

	<-ctx.Done()

	// Wait for all to complete
	timeout := time.After(3 * time.Second)
	for completed.Load() < numFuncs {
		select {
		case <-timeout:
			t.Fatalf("only %d/%d functions completed", completed.Load(), numFuncs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestContextCancelDuringDBOperation tests context cancellation during database operations.
func TestContextCancelDuringDBOperation(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create many tasks to make DB operation take longer
	for i := 0; i < 100; i++ {
		task := &repo.Task{
			TaskID:    fmt.Sprintf("task-%d", i),
			Platform:  constant.TaskPlatformSuno,
			Status:    "PENDING",
			Progress:  "0%",
			ChannelId: 1,
		}
		repo.DB.Create(task)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		UpdateTaskBulkWithContext(ctx)
		close(done)
	}()

	// Cancel almost immediately
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not respond to cancel")
	}
}

// TestOperationSettingIntegration tests integration with operation settings.
func TestOperationSettingIntegration(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()

	// Store a test monitor setting
	setting := operation_setting.MonitorSetting{
		AutoTestChannelEnabled: false,
		AutoTestChannelMinutes: 60,
	}
	settingJSON, _ := json.Marshal(setting)
	repo.DB.Create(&repo.Option{
		Key:   "MonitorSetting",
		Value: string(settingJSON),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		AutomaticallyTestChannelsWithContext(ctx)
		close(done)
	}()

	<-ctx.Done()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("function did not exit after context timeout")
	}
}

// TestUpdateMidjourneyTasks_DirectCall tests the updateMidjourneyTasks helper function directly.
func TestUpdateMidjourneyTasks_DirectCall_EmptyTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	ctx := context.Background()
	// Should not panic with empty tasks
	updateMidjourneyTasks(ctx)
}

// TestUpdateMidjourneyTasks_DirectCall_WithTasks tests updateMidjourneyTasks with actual tasks.
func TestUpdateMidjourneyTasks_DirectCall_WithTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create tasks with empty MjId (null task IDs)
	tasks := []*repo.Midjourney{
		{
			MjId:      "",
			ChannelId: 1,
			Status:    "PENDING",
			Progress:  "0%",
		},
		{
			MjId:      "",
			ChannelId: 1,
			Status:    "PENDING",
			Progress:  "0%",
		},
	}
	for _, task := range tasks {
		if err := repo.DB.Create(task).Error; err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
	}

	ctx := context.Background()
	updateMidjourneyTasks(ctx)

	// Verify tasks were updated to FAILURE
	var updatedTasks []repo.Midjourney
	repo.DB.Find(&updatedTasks)
	for _, task := range updatedTasks {
		if task.Status != "FAILURE" {
			t.Errorf("expected status FAILURE, got %s", task.Status)
		}
	}
}

// TestUpdateMidjourneyTasks_DirectCall_WithValidTasks tests updateMidjourneyTasks with valid MjId.
// This test verifies the code path when tasks have valid MjId but channel is not cached.
func TestUpdateMidjourneyTasks_DirectCall_WithValidTasks(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create tasks with valid MjId but for a non-existent channel
	// This will trigger the CacheGetChannel error path
	tasks := []*repo.Midjourney{
		{
			MjId:      "mj-valid-1",
			ChannelId: 999, // Non-existent channel
			Status:    "PENDING",
			Progress:  "0%",
		},
	}
	for _, task := range tasks {
		if err := repo.DB.Create(task).Error; err != nil {
			t.Fatalf("failed to create task: %v", err)
		}
	}

	ctx := context.Background()
	// This will hit the CacheGetChannel error path and set tasks to FAILURE
	updateMidjourneyTasks(ctx)

	// Verify task was updated to FAILURE due to channel not found
	var updatedTask repo.Midjourney
	repo.DB.First(&updatedTask)
	if updatedTask.Status != "FAILURE" {
		t.Errorf("expected status FAILURE, got %s", updatedTask.Status)
	}
}

// TestUpdateMidjourneyTasks_ContextCancellation tests cancellation during updateMidjourneyTasks.
func TestUpdateMidjourneyTasks_ContextCancellation(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	// Create many channels to increase chance of hitting cancellation check
	for i := 0; i < 10; i++ {
		channel := &repo.Channel{
			Name:    fmt.Sprintf("test-channel-%d", i),
			Type:    1,
			Key:     fmt.Sprintf("key-%d", i),
			Status:  common.ChannelStatusEnabled,
			BaseURL: stringPtr("http://localhost:8080"),
		}
		repo.DB.Create(channel)

		task := &repo.Midjourney{
			MjId:      fmt.Sprintf("mj-task-%d", i),
			ChannelId: channel.Id,
			Status:    "PENDING",
			Progress:  "0%",
		}
		repo.DB.Create(task)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	// Should exit early due to context cancellation
	updateMidjourneyTasks(ctx)
}

func stringPtr(s string) *string {
	return &s
}

// Benchmark integration tests
func BenchmarkUpdateTaskWithDB_Cancel(b *testing.B) {
	// This benchmark uses real DB
	dbName := "file:benchtest?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}
	db.AutoMigrate(&repo.Task{})

	prevDB := repo.DB
	repo.DB = db
	defer func() { repo.DB = prevDB }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		UpdateTaskBulkWithContext(ctx)
	}
}

func BenchmarkUpdateMidjourneyWithDB_Cancel(b *testing.B) {
	dbName := "file:benchtest2?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}
	db.AutoMigrate(&repo.Midjourney{}, &repo.Channel{})

	prevDB := repo.DB
	repo.DB = db
	defer func() { repo.DB = prevDB }()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		UpdateMidjourneyTaskBulkWithContext(ctx)
	}
}

// TestChannelHealthTest_StartRegistersHeartbeat is the A-F1 oracle for
// channel-health-test: the boot-time Set(0) and taskreg.Register calls
// inside AutomaticallyTestChannelsWithContext are otherwise deletable with
// every test in this package staying green (TestChannelHealthTest_SuccessfulTickStampsHeartbeat
// above only observes a completed tick, not the pre-loop registration).
// Pre-stamps a distinctive non-zero value so the zero-assertion below
// cannot pass merely from a GaugeVec's first-access default.
func TestChannelHealthTest_StartRegistersHeartbeat(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()

	metrics.LeaderTaskLastSuccess.WithLabelValues(channelHealthTestTaskName).Set(999999999)
	before := len(taskreg.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	AutomaticallyTestChannelsWithContext(ctx)

	if got := testutil.ToFloat64(metrics.LeaderTaskLastSuccess.WithLabelValues(channelHealthTestTaskName)); got != 0 {
		t.Errorf("LeaderTaskLastSuccess{task=channel-health-test} = %v immediately after AutomaticallyTestChannelsWithContext, want 0 (boot-time Set(0) resetting a pre-stamped series)", got)
	}

	snap := taskreg.Snapshot()
	if len(snap) <= before {
		t.Fatalf("taskreg.Snapshot() length did not grow: before=%d after=%d", before, len(snap))
	}
	found := false
	for _, task := range snap {
		if task.Name == channelHealthTestTaskName {
			found = true
			// LeaderOnly flipped to true in cycle-11 L3: before this fix
			// every master-capable replica ran its own full probe pass on
			// every tick, each with independent ban/enable authority.
			if !task.LeaderOnly {
				t.Errorf("%s task.LeaderOnly = false, want true (only the leader runs the probe pass)", channelHealthTestTaskName)
			}
			if task.Active == nil {
				t.Errorf("%s task.Active = nil, want a non-nil func (B-F1: must report standby/disabled when AutoTestChannelEnabled is off)", channelHealthTestTaskName)
			}
		}
	}
	if !found {
		t.Errorf("taskreg.Snapshot() does not contain %q after AutomaticallyTestChannelsWithContext", channelHealthTestTaskName)
	}
}

// TestChannelHealthTest_ActiveFuncReflectsAutoTestSetting is the B-F1
// oracle at the registration layer (the handler-level oracle lives in
// v2_admin_system_tasks_test.go's TestSystemTasks_DisabledJobIsNotOverdue,
// using a synthetic task — this drives the REAL registered Active func
// channel-test.go builds). AutoTestChannelEnabled defaults to false
// (operation_setting/monitor_setting.go), so on a default install this
// task's Active() must read false; flipping the setting on must flip it to
// true, and AutoTestChannelMinutes<=0 must keep it false even when enabled
// is true (a 0-minute interval cannot be "active" — GetSystemTasksV2 divides
// by it).
func TestChannelHealthTest_ActiveFuncReflectsAutoTestSetting(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevMaster := common.IsMasterNode
	common.IsMasterNode = true
	defer func() { common.IsMasterNode = prevMaster }()

	ms := operation_setting.GetMonitorSetting()
	prevEnabled, prevMinutes := ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes
	t.Cleanup(func() { ms.AutoTestChannelEnabled, ms.AutoTestChannelMinutes = prevEnabled, prevMinutes })

	ms.AutoTestChannelEnabled = false
	ms.AutoTestChannelMinutes = 10

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	AutomaticallyTestChannelsWithContext(ctx)

	snap := taskreg.Snapshot()
	var active func() bool
	for _, task := range snap {
		if task.Name == channelHealthTestTaskName {
			active = task.Active // last registration wins if this ran more than once in the binary
		}
	}
	if active == nil {
		t.Fatalf("%s not registered, or registered with a nil Active func", channelHealthTestTaskName)
	}

	if got := active(); got {
		t.Errorf("Active() = true with AutoTestChannelEnabled=false, want false")
	}

	ms.AutoTestChannelEnabled = true
	if got := active(); !got {
		t.Errorf("Active() = false with AutoTestChannelEnabled=true, AutoTestChannelMinutes=10, want true")
	}

	ms.AutoTestChannelMinutes = 0
	if got := active(); got {
		t.Errorf("Active() = true with AutoTestChannelMinutes=0, want false (a 0-minute interval cannot be active)")
	}
}

// TestChannelHealthTest_StampMeansLaunchedNotCompleted proves the B-F10 doc
// comment claim: the heartbeat call site mirrored below — the same shape as
// AutomaticallyTestChannelsWithContext's `if err := testAllChannels(false);
// err == nil { metrics.RecordLeaderTaskSuccess(...) }` — stamps when
// testAllChannels returns nil, i.e. when the async pass over every channel
// is LAUNCHED (handed to gopool.Go), not when that pass actually finishes.
// Seeds several Midjourney-type channels (an unsupported test type:
// testChannel returns an immediate, network-free localErr — see
// channel-test.go's unsupportedTestChannelTypes) with common.RequestInterval
// inflated well beyond the launch+stamp round trip, so the async pass is
// still measurably running (testAllChannelsRunning still true) at the
// moment the stamp lands.
func TestChannelHealthTest_StampMeansLaunchedNotCompleted(t *testing.T) {
	cleanup := setupContextTestDB(t)
	defer cleanup()

	prevInterval := common.RequestInterval
	common.RequestInterval = 200 * time.Millisecond
	defer func() { common.RequestInterval = prevInterval }()

	for i := 0; i < 4; i++ {
		ch := &repo.Channel{
			Name:   fmt.Sprintf("mj-slow-%d", i),
			Type:   constant.ChannelTypeMidjourney,
			Status: common.ChannelStatusEnabled,
		}
		if err := repo.DB.Create(ch).Error; err != nil {
			t.Fatalf("seed channel %d: %v", i, err)
		}
	}
	// Total pass duration is >= 4*200ms=800ms (one RequestInterval sleep per
	// channel, including the last). testAllChannels itself must return long
	// before that: it only launches the gopool.Go goroutine.

	launchStart := time.Now()
	if err := testAllChannels(false); err != nil {
		t.Fatalf("testAllChannels() error = %v", err)
	}
	metrics.RecordLeaderTaskSuccess(channelHealthTestTaskName) // mirrors the production call site
	launchElapsed := time.Since(launchStart)

	testAllChannelsLock.Lock()
	stillRunning := testAllChannelsRunning
	testAllChannelsLock.Unlock()

	// Drain before returning: the launched pass outlives this function by the
	// better part of a second and keeps using repo.DB, which this test's
	// cleanup is about to swap out from under it. Waiting here is also why
	// the timing assertion above is taken first.
	defer func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			testAllChannelsLock.Lock()
			running := testAllChannelsRunning
			testAllChannelsLock.Unlock()
			if !running {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Log("async channel-test pass still running after 10s; leaving it to finish against the restored DB")
	}()

	if launchElapsed > 150*time.Millisecond {
		t.Fatalf("testAllChannels()+stamp took %s, want well under the ~800ms async pass duration — the stamp must not block on the pass completing", launchElapsed)
	}
	if !stillRunning {
		t.Skip("async pass already finished before the stamp — timing too tight on this machine to distinguish launch from completion semantics; not a functional assertion failure")
	}
}
