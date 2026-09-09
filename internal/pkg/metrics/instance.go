package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// These three series make replica-level attribution possible: which replica
// holds the HA leader lease, whether each leader-gated periodic task is
// actually running, and which pod answered a scrape that passed the /metrics
// auth gate. Before this a follower answering /api/health looked identical to
// the leader, and a scrape on the shared NodePort could not be tied to a
// specific pod.
var (
	// Leader is 1 while this process holds the HA leader lease, 0 otherwise.
	// The single writer is common.SetLeader, which every leadership-changing
	// call site (boot lease acquisition, LeaderManager.step, release on
	// shutdown) already goes through.
	Leader = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "leader",
		Help:      "1 if this process currently holds the HA leader lease, 0 otherwise",
	})

	// LeaderTaskLastSuccess is the unix timestamp of the last successful run
	// of a leader-gated periodic task (lifecycle.LeaderTask), keyed by task
	// name. The sole NewLeaderTask caller today is secret rotation
	// (secret_rotation.go:27). lifecycle.NewLeaderTask initialises this
	// series to 0 for its task name at registration, before the task has
	// ever run — a GaugeVec exports no series at all for a label it has
	// never been set with, so without that a task that never once succeeds
	// would be invisible to a `time() - last_success > X` alert rather than
	// visibly stuck at 0.
	LeaderTaskLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "leader_task_last_success_timestamp_seconds",
		Help:      "Unix timestamp of the last successful run of a leader-gated periodic task",
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

// RecordLeaderTaskSuccess stamps "now" as the last successful run of the
// named leader-gated task. Call only when the task's fn returned a nil
// error — see lifecycle.LeaderTask.Run.
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
