package handler

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/metrics"
	"github.com/LurusTech/lurus-hub/internal/pkg/taskreg"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// systemTaskOverdueMultiple is how many missed intervals before a task's
// heartbeat is reported "overdue" rather than "ok". 2x tolerates one slow
// tick without paging on noise while still catching a genuinely stuck task
// well before an operator would otherwise notice.
const systemTaskOverdueMultiple = 2

// systemTasksNow is GetSystemTasksV2's clock, indirected for tests (L3
// repair round, B-F9): TestSystemTasks_OverdueBoundary previously compared a
// gauge stamped against the real clock to common.GetTimestamp() read a
// moment later, which is flaky if a second boundary elapses between the two
// reads. Tests override this var; production leaves it as common.GetTimestamp.
var systemTasksNow = common.GetTimestamp

// systemTaskView is one row of GET /api/v2/admin/system/tasks.
type systemTaskView struct {
	Name            string  `json:"name"`
	IntervalSeconds float64 `json:"interval_seconds"`
	LeaderOnly      bool    `json:"leader_only"`
	LastSuccessAt   int64   `json:"last_success_at"`
	// State is one of "ok" | "overdue" | "standby" — never a bare bool.
	// "standby" covers two distinct reasons, told apart by StandbyReason:
	// leader_only is true and this replica isn't currently the leader (it
	// has never been this task's job to stamp the heartbeat), or the task
	// is administratively inactive (e.g. channel-health-test with auto
	// channel test turned off). Conflating either with "overdue" would page
	// an operator on every follower in a healthy deployment, or on every
	// default install with a job intentionally left off.
	State string `json:"state"`
	// StandbyReason is "" unless State == "standby", in which case it is
	// "follower" (leader-gated task, this replica is not the leader) or
	// "disabled" (task.Active() returned false — operator turned it off).
	StandbyReason string `json:"standby_reason"`
}

// GetSystemTasksV2 answers GET /api/v2/admin/system/tasks (root-only,
// mounted under adminRoute / RootJWTAuth in api-v2-router.go).
//
// This is a PER-POD health view, not a cluster-wide aggregator: taskreg and
// metrics.LeaderTaskLastSuccess are both process-local, so it reports only
// what THIS replica has registered and stamped — the same convention
// GetGatewayHealthV2 already uses for per-replica circuit-breaker state.
// Cross-replica drift (e.g. every replica but one silently crash-looping
// before it can register) is invisible to a single call of this endpoint;
// an operator comparing it across replicas, or the Prometheus series
// directly, is how that gets caught.
func GetSystemTasksV2(c *gin.Context) {
	now := systemTasksNow()
	isLeader := common.IsLeader()
	pod := common.InstanceID()

	registered := taskreg.Snapshot()
	views := make([]systemTaskView, 0, len(registered))
	for _, task := range registered {
		intervalSeconds := task.Interval().Seconds()
		lastSuccess := readGaugeValue(metrics.LeaderTaskLastSuccess.WithLabelValues(task.Name))
		active := task.Active == nil || task.Active()

		var state, reason string
		switch {
		case !active:
			// The operator turned this job off (e.g. channel-health-test
			// with AutoTestChannelEnabled=false, the code default). A
			// disabled job cannot be late for a schedule it isn't on —
			// never overdue (L3 repair round, B-F1).
			state, reason = "standby", "disabled"
		case task.LeaderOnly && !isLeader:
			// A follower never runs this task's body at all — last_success_at
			// staying at 0 here is expected, not a fault.
			state, reason = "standby", "follower"
		case lastSuccess > 0:
			if float64(now)-lastSuccess > systemTaskOverdueMultiple*intervalSeconds {
				state = "overdue"
			} else {
				state = "ok"
			}
		default:
			// Never once succeeded on this replica (lastSuccess == 0), the
			// task is active, and either not leader-gated or leader-gated
			// with this replica currently the leader — judge against
			// uptime instead, so a task that hasn't had time for its first
			// tick yet reads "ok", not "overdue" the instant the pod boots.
			//
			// For leader-only tasks the uptime window starts at
			// max(StartTime, LeaderSince) rather than raw process start
			// (L3 repair round, B-F5): a replica that ran for days as a
			// follower and only just won the lease has had zero chances to
			// run a 24h leader-only job, and must not be reported overdue
			// for up to 24h the moment it takes over.
			windowStart := common.StartTime
			if task.LeaderOnly {
				if since := common.LeaderSince(); since > windowStart {
					windowStart = since
				}
			}
			uptime := now - windowStart
			if float64(uptime) > systemTaskOverdueMultiple*intervalSeconds {
				state = "overdue"
			} else {
				state = "ok"
			}
		}

		views = append(views, systemTaskView{
			Name:            task.Name,
			IntervalSeconds: intervalSeconds,
			LeaderOnly:      task.LeaderOnly,
			LastSuccessAt:   int64(lastSuccess),
			State:           state,
			StandbyReason:   reason,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"is_leader": isLeader,
			"pod":       pod,
			"tasks":     views,
		},
	})
}

// readGaugeValue reads a gauge's current value through the client library's
// own Write(&dto.Metric{}) — the same mechanism prometheus/client_golang's
// testutil.ToFloat64 uses internally — rather than scraping and parsing the
// /metrics text exposition format. A zero value is indistinguishable from
// "never set" by design: metrics.LeaderTaskLastSuccess.WithLabelValues
// creates the series on first access (GaugeVec semantics), and each
// registered task's Start*/Auto*WithContext entry point (or
// lifecycle.NewLeaderTask, for the jobs that use it) explicitly Set(0)s it
// before this handler is expected to observe it, so Write erroring here
// would mean something is wrong with the metric itself, not a legitimate
// "unknown" case.
func readGaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return 0
	}
	return m.GetGauge().GetValue()
}
