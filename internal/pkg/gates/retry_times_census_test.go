package gates

// The RetryTimes census rows live beside TestEveryRetryTimesConsumerIsAccountedFor
// in shipped_defaults_test.go; they are split out to keep that file under its
// Organic Architecture length ceiling.

// retryTimesConsumers is the census. Raising RetryTimes from 0 to 2 switched on
// every loop below at once, so every one of them needs a sentence here.
func retryTimesConsumers() []retryTimesConsumer {
	return []retryTimesConsumer{
		{
			file: "internal/adapter/handler/relay_auxiliary.go",
			refs: 1,
			why: "RelayTask, the async task relay, split out of relay.go. `retryTimes := " +
				"common.RetryTimes` is its budget copy: at the old default of 0 its loop " +
				"body never ran, and raising the budget switched it on. It re-SUBMITS a " +
				"video / music / Suno job, which is not idempotent, and shouldRetryTaskRelay " +
				"tests the status code before LocalError, so our own local 500s retry too " +
				"— including `insert_task_failed`, which is raised AFTER adaptor.DoRequest " +
				"has already created the job at the vendor. A retry there creates a second " +
				"vendor job. See constants.go for why that is currently accepted and what " +
				"would scope it.",
		},
		{
			file: "internal/adapter/handler/relay.go",
			refs: 3,
			why: "TWO loops, not one. Three references are RelayHandler's synchronous " +
				"failover loop (the header `retryParam.GetRetry() <= common.RetryTimes`, " +
				"the remaining budget handed to shouldRetry, and the same subtraction on " +
				"the channel-selection-error branch). The FOURTH, " +
				"`retryTimes := common.RetryTimes` in RelayTask, is the async task relay: " +
				"at the old default of 0 its loop body never ran, and raising the budget " +
				"switched it on. It re-SUBMITS a video / music / Suno job, which is not " +
				"idempotent, and shouldRetryTaskRelay tests the status code before " +
				"LocalError, so our own local 500s retry too — including " +
				"`insert_task_failed`, which is raised AFTER adaptor.DoRequest has " +
				"already created the job at the vendor. A retry there creates a second " +
				"vendor job. See constants.go for why that is currently accepted and " +
				"what would scope it.",
		},
		{
			file: "internal/app/channel_select.go",
			refs: 2,
			why: "The selector, and the reason the attempt bound is PER GROUP. For an " +
				"`auto`-group token with cross-group retry it compares the in-group " +
				"retry index against the budget and, when the group is exhausted, calls " +
				"param.SetRetry(0) + param.ResetRetryNextTry() so the outer loop starts " +
				"a fresh budget on the next group. Any sentence of the form 'total " +
				"upstream attempts = RetryTimes + 1 per request' is false while these " +
				"two lines exist; TestAutoGroupRetryBudgetResetIsStillThere pins them.",
		},
		{
			file: "internal/adapter/repo/option.go",
			refs: 3,
			why: "The options-table plumbing, not a consumer: InitOptionMap seeds " +
				"OptionMap[\"RetryTimes\"] from the compiled value so the console shows " +
				"what is running, and updateOptionMap's `RetryTimes` case applies a row " +
				"through optionInt (previous value kept on a parse failure). optionInt " +
				"accepts a NEGATIVE integer and RetryTimes is not in " +
				"positiveRangeOptionKinds, so `RetryTimes = -1` makes RelayHandler's " +
				"loop body never execute and every relay answers the all-breakers-open " +
				"503 — see the warning in doc/runbook/shipped-defaults.md.",
		},
	}
}
