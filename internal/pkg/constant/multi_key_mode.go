package constant

type MultiKeyMode string

const (
	MultiKeyModeRandom   MultiKeyMode = "random"   // 随机
	MultiKeyModePolling  MultiKeyMode = "polling"  // 轮询
	MultiKeyModeWeighted MultiKeyMode = "weighted" // 加权轮转(per-key weight 0-100)
)
