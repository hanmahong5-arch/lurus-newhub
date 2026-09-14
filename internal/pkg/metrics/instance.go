package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// These three series make replica-level attribution possible: which replica
// holds the HA leader lease, whether each registered periodic background
// task last succeeded, and which pod answered a scrape that passed the
// /metrics auth gate. Before this a follower answering /api/health looked
// identical to the leader, and a scrape on the shared NodePort could not be
// tied to a specific pod.
var (
	// Leader is 1 while this process holds the HA leader lease, 0 otherwise.
	// Written by common.SetLeader, which the leadership-changing call sites
	// (boot lease acquisition, LeaderManager.step, release on shutdown) go
	// through.
	Leader = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "leader",
		Help:      "1 if this process currently holds the HA leader lease, 0 otherwise",
	})

	// LeaderTaskLastSuccess is the unix timestamp of the last successful run
	// of a periodic background task, keyed by task name. Not every periodic
	// loop in this codebase participates — e.g. UpdateTaskBulkWithContext,
	// the billing outbox loop and the daily quota reset cron (cmd/server/
	// main.go) do not stamp this gauge at all. Among the ones that do:
	// lifecycle.NewLeaderTask callers (secret rotation, session sweep,
	// response-registry sweep) get this stamped automatically on every
	// successful fn. Raw-ticker jobs that are NOT NewLeaderTask-wrapped
	// (audit cleanup, privacy erasure, credit-pool reconcile, the
	// OpenRouter pool reaper, channel health test — L3, 2026-09-13) call
	// metrics.RecordLeaderTaskSuccess directly from inside their own tick
	// body and Set(0) at their Start*WithContext entry point for the same
	// reason NewLeaderTask does it below: a GaugeVec exports no series at
	// all for a label it has never been set with, so without the boot-time
	// zero, a task that never once succeeds would be invisible to a
	// `time() - last_success > X` alert rather than visibly stuck at 0.
	// Among the jobs that stamp this gauge, channel-health-test is not
	// leader-gated — it runs on every master-capable replica
	// (common.IsMasterNode), not just the HA leader — so an alert on this
	// series must not blanket-qualify with `lurus_gateway_leader == 1`
	// (see doc/runbook/ha-deployment.md; per-task leader_only is queryable
	// at GET /api/v2/admin/system/tasks). internal/pkg/taskreg pairs this
	// gauge with each task's static interval/leaderOnly/active metadata for
	// that endpoint.
	LeaderTaskLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "leader_task_last_success_timestamp_seconds",
		Help:      "Unix timestamp of the last successful run of a periodic background task; for tasks that only dispatch async work before returning (e.g. channel-health-test), this marks launch rather than completion",
	}, []string{"task"})

	// InstanceInfo is a Prometheus "info" gauge (constant 1) carrying this
	// pod's identity as labels — pod/namespace/version, naming inspired by
	// (not a rendering of) the OTel semconv service.instance.id vocabulary.
	// Set once at boot, before /metrics is mounted.
	InstanceInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "instance_info",
		Help:      "Constant 1, labeled with this instance's pod, namespace and version",
	}, []string{"pod", "namespace", "version"})
)

// SetLeader publishes the current HA leadership state. Called from
// common.SetLeader.
func SetLeader(held bool) {
	if held {
		Leader.Set(1)
	} else {
		Leader.Set(0)
	}
}

// RecordLeaderTaskSuccess stamps "now" as the last successful run (or, for
// tasks whose call site only dispatches async work before returning, last
// successful launch — see channel-health-test) of the named periodic
// background task. Call only when the task's own pass reported no error.
// Callers include lifecycle.LeaderTask.Run for NewLeaderTask-wrapped jobs
// and several raw-ticker jobs (audit cleanup, privacy erasure, credit-pool
// reconcile, the OpenRouter pool reaper, channel-health-test) that call it
// directly from their own tick body; not every caller is leader-gated —
// see taskreg's LeaderOnly field for which ones are.
func RecordLeaderTaskSuccess(task string) {
	LeaderTaskLastSuccess.WithLabelValues(task).Set(float64(time.Now().Unix()))
}

// SetInstanceInfo publishes this pod's identity. Called once from
// router.SetRouter before /metrics is mounted; Reset first so a hypothetical
// second call (there is none in production today) can't leave a stale label
// set alongside the new one.
func SetInstanceInfo(pod, podNamespace, version string) {
	InstanceInfo.Reset()
	InstanceInfo.WithLabelValues(pod, podNamespace, version).Set(1)
}
