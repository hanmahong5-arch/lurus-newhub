package handler

// task_video_test.go — a video task that comes back with real token usage is
// re-settled, and a re-settlement is a charge like any other: it has to move
// every ledger the submission moved.
//
// Before cycle 13 the top-up branch called repo.DecreaseUserQuota directly,
// so the per-key allowance and the tenant pool kept the ESTIMATE forever
// while the user's balance carried the actual cost, and the difference was
// recorded as a system log row — which no invoice counts — leaving the
// customer's consume rows summing to the estimate rather than to what they
// were charged.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/provider"
	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/constant"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/LurusTech/lurus-hub/internal/pkg/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// fakeVideoTaskAdaptor answers the poll with a fixed upstream result. Only
// FetchTask and ParseTaskResult are reached by updateVideoSingleTask; the
// rest of provider.TaskAdaptor is stubbed so the fake satisfies the real
// interface (a narrower local interface would let the test keep compiling
// after the production signature moved on).
type fakeVideoTaskAdaptor struct {
	body   string
	result *relaycommon.TaskInfo
}

func (f *fakeVideoTaskAdaptor) Init(info *relaycommon.RelayInfo) {}

func (f *fakeVideoTaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	return nil
}

func (f *fakeVideoTaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return "", nil
}

func (f *fakeVideoTaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	return nil
}

func (f *fakeVideoTaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	return nil, nil
}

func (f *fakeVideoTaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return nil, nil
}

func (f *fakeVideoTaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	return "", nil, nil
}

func (f *fakeVideoTaskAdaptor) GetModelList() []string { return []string{"gpt-4o"} }

func (f *fakeVideoTaskAdaptor) GetChannelName() string { return "fake-video" }

func (f *fakeVideoTaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string, forceHTTP1 ...bool) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(f.body)),
	}, nil
}

func (f *fakeVideoTaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	return f.result, nil
}

var _ provider.TaskAdaptor = (*fakeVideoTaskAdaptor)(nil)

// videoResettleQuota is the charge the production code derives for a finished
// video task: totalTokens * modelRatio * groupRatio. Read through the same
// settings the code reads so the test pins the LEDGER MOVEMENT, not a frozen
// ratio table.
func videoResettleQuota(t *testing.T, model string, group string, totalTokens int) int {
	t.Helper()
	modelRatio, hasRatio, _ := ratio_setting.GetModelRatio(model)
	if !hasRatio || modelRatio <= 0 {
		t.Fatalf("model %q has no usable ratio (%v, %v) — re-settlement would be skipped and this test would prove nothing",
			model, modelRatio, hasRatio)
	}
	groupRatio := ratio_setting.GetGroupRatio(group)
	if userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(group, group); ok {
		groupRatio = userGroupRatio
	}
	return int(float64(totalTokens) * modelRatio * groupRatio)
}

func TestUpdateVideoSingleTask_ResettleMovesEveryLedger(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate tasks: %v", err)
	}

	f := seedTaskLedgerFixture(t, ctx, "video-resettle")
	const estimate = 5_000
	f.chargeLikeTaskSubmission(t, estimate)

	const model = "gpt-4o"
	const totalTokens = 8_000
	actualQuota := videoResettleQuota(t, model, "default", totalTokens)
	if actualQuota <= estimate {
		t.Fatalf("actual quota %d must exceed the estimate %d for this test to exercise the top-up branch",
			actualQuota, estimate)
	}

	task := &repo.Task{
		TaskID:     "vid-" + common.GetRandomString(8),
		Platform:   constant.TaskPlatform("video"),
		UserId:     ctx.NormalUser.Id,
		ChannelId:  f.channel.Id,
		Group:      "default",
		Quota:      estimate,
		Action:     constant.TaskActionGenerate,
		Status:     repo.TaskStatusInProgress,
		Progress:   "30%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	adaptor := &fakeVideoTaskAdaptor{
		// The poll body is what the code re-reads the model name from.
		body: `{"model":"` + model + `"}`,
		result: &relaycommon.TaskInfo{
			TaskID:      task.TaskID,
			Status:      string(repo.TaskStatusSuccess),
			TotalTokens: totalTokens,
		},
	}

	if err := updateVideoSingleTask(context.Background(), adaptor, f.channel, task.TaskID,
		map[string]*repo.Task{task.TaskID: task}); err != nil {
		t.Fatalf("updateVideoSingleTask: %v", err)
	}

	if task.Quota != actualQuota {
		t.Errorf("task.Quota = %d, want %d — the row must record what was actually charged", task.Quota, actualQuota)
	}

	if quota, _, _ := readUserCounters(t, ctx, ctx.NormalUser.Id); quota != f.startUserQuota-actualQuota {
		t.Errorf("user quota = %d, want %d", quota, f.startUserQuota-actualQuota)
	}
	if got := f.readTokenRemain(t); got != f.startTokenRemain-actualQuota {
		t.Errorf("token remain_quota = %d, want %d — the per-key allowance still carries the estimate, "+
			"so the key's cap no longer reflects what the customer spent", got, f.startTokenRemain-actualQuota)
	}
	if got := f.readPoolBalance(t); got != f.startPoolBalance-int64(actualQuota) {
		t.Errorf("pool balance = %d, want %d — the tenant pool still carries the estimate",
			got, f.startPoolBalance-int64(actualQuota))
	}

	var consumeSum int64
	if err := ctx.DB.Model(&repo.Log{}).
		Where("user_id = ? AND type = ?", ctx.NormalUser.Id, repo.LogTypeConsume).
		Select("COALESCE(SUM(quota), 0)").Scan(&consumeSum).Error; err != nil {
		t.Fatalf("sum consume rows: %v", err)
	}
	if consumeSum != int64(actualQuota) {
		t.Errorf("consume rows sum to %d, want %d — every invoice and usage report reads these rows, "+
			"so a re-settlement recorded anywhere else bills the estimate forever", consumeSum, actualQuota)
	}
}

// TestUpdateVideoSingleTask_ResettleRefundReversesEveryLedger is the mirror
// image: an over-estimated video task hands the difference back on the same
// three ledgers it was taken from.
func TestUpdateVideoSingleTask_ResettleRefundReversesEveryLedger(t *testing.T) {
	ctx := SetupV2TestRouter(t)
	defer ctx.Cleanup()

	if err := ctx.DB.AutoMigrate(&repo.Task{}); err != nil {
		t.Fatalf("migrate tasks: %v", err)
	}

	f := seedTaskLedgerFixture(t, ctx, "video-refund")

	const model = "gpt-4o"
	const totalTokens = 100
	actualQuota := videoResettleQuota(t, model, "default", totalTokens)
	estimate := actualQuota + 3_000

	f.chargeLikeTaskSubmission(t, estimate)

	task := &repo.Task{
		TaskID:     "vid-" + common.GetRandomString(8),
		Platform:   constant.TaskPlatform("video"),
		UserId:     ctx.NormalUser.Id,
		ChannelId:  f.channel.Id,
		Group:      "default",
		Quota:      estimate,
		Action:     constant.TaskActionGenerate,
		Status:     repo.TaskStatusInProgress,
		Progress:   "30%",
		SubmitTime: common.GetTimestamp(),
	}
	if err := task.Insert(); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	adaptor := &fakeVideoTaskAdaptor{
		body: `{"model":"` + model + `"}`,
		result: &relaycommon.TaskInfo{
			TaskID:      task.TaskID,
			Status:      string(repo.TaskStatusSuccess),
			TotalTokens: totalTokens,
		},
	}

	if err := updateVideoSingleTask(context.Background(), adaptor, f.channel, task.TaskID,
		map[string]*repo.Task{task.TaskID: task}); err != nil {
		t.Fatalf("updateVideoSingleTask: %v", err)
	}

	if got := f.readTokenRemain(t); got != f.startTokenRemain-actualQuota {
		t.Errorf("token remain_quota = %d, want %d — the over-estimate was never handed back to the key",
			got, f.startTokenRemain-actualQuota)
	}
	if got := f.readPoolBalance(t); got != f.startPoolBalance-int64(actualQuota) {
		t.Errorf("pool balance = %d, want %d — the over-estimate was never handed back to the pool",
			got, f.startPoolBalance-int64(actualQuota))
	}
	if quota, _, _ := readUserCounters(t, ctx, ctx.NormalUser.Id); quota != f.startUserQuota-actualQuota {
		t.Errorf("user quota = %d, want %d", quota, f.startUserQuota-actualQuota)
	}
}
