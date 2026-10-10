package app

// RefreshChannelCooldownSnapshot reloads the cross-replica cooldown view with
// the single index read the selector already uses (rate-limited to once per
// cooldownSnapshotEvery). Ops views that evaluate many channels call it once,
// after which ChannelCoolingUntil is a pure in-memory lookup per channel.
func RefreshChannelCooldownSnapshot() { refreshCooldownSnapshot() }
