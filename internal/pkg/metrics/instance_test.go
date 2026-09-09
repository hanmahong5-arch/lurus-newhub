package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSetLeader_PublishesGauge pins the choke point common.SetLeader relies
// on: flipping leadership must move the gauge both ways.
func TestSetLeader_PublishesGauge(t *testing.T) {
	SetLeader(true)
	if got := testutil.ToFloat64(Leader); got != 1 {
		t.Errorf("Leader = %v after SetLeader(true), want 1", got)
	}
	SetLeader(false)
	if got := testutil.ToFloat64(Leader); got != 0 {
		t.Errorf("Leader = %v after SetLeader(false), want 0", got)
	}
}

// TestRecordLeaderTaskSuccess_StampsRecentTimestamp pins the {task} series
// LeaderTask.Run writes on every successful pass.
func TestRecordLeaderTaskSuccess_StampsRecentTimestamp(t *testing.T) {
	before := time.Now().Unix()
	RecordLeaderTaskSuccess("secret-rotation")
	after := time.Now().Unix()

	got := testutil.ToFloat64(LeaderTaskLastSuccess.WithLabelValues("secret-rotation"))
	if got < float64(before-2) || got > float64(after+2) {
		t.Errorf("LeaderTaskLastSuccess{task=secret-rotation} = %v, want within 2s of now (%d..%d)", got, before, after)
	}
}

// TestSetInstanceInfo_PublishesLabeledConstant pins the info-gauge shape:
// constant 1, labeled with the identity SetInstanceInfo was called with.
func TestSetInstanceInfo_PublishesLabeledConstant(t *testing.T) {
	SetInstanceInfo("test-pod-a", "lurus-newhub-uat", "v9.9.9")

	got := testutil.ToFloat64(InstanceInfo.WithLabelValues("test-pod-a", "lurus-newhub-uat", "v9.9.9"))
	if got != 1 {
		t.Errorf("InstanceInfo{pod=test-pod-a,...} = %v, want 1", got)
	}
}

// TestSetInstanceInfo_SecondCallLeavesExactlyOneSeries locks the Reset()
// call inside SetInstanceInfo: there is no second production call site
// today, but if one is ever added (or a process re-publishes after a config
// reload), calling it twice with different labels must not leave the old
// label set alongside the new one — that would make this pod appear as two
// different pods to any consumer scraping lurus_gateway_instance_info.
func TestSetInstanceInfo_SecondCallLeavesExactlyOneSeries(t *testing.T) {
	SetInstanceInfo("pod-old", "ns-a", "v1")
	SetInstanceInfo("pod-new", "ns-b", "v2")

	ch := make(chan prometheus.Metric, 16)
	InstanceInfo.Collect(ch)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	if count != 1 {
		t.Errorf("InstanceInfo has %d series after a second SetInstanceInfo call, want exactly 1", count)
	}
}
