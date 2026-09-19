package repo

// ability_sole_channel.go — L3 (cycle 11): the seam
// channel_probe_policy.go's evaluateProbeOutcome consults before letting a
// third consecutive latency breach turn into an automatic ban, so a
// channel that is the ONLY enabled route for some (group, model) pair
// never gets auto-disabled out from under every caller of that pair.

// SoleModel is one (group, model) pair a channel serves for which no other
// enabled channel, visible to the same tenant scope, can also serve it.
type SoleModel struct {
	Group string
	Model string
}

// SoleEnabledModelsForChannel returns the (group, model) pairs channelID
// serves (its own enabled ability rows) for which no OTHER enabled
// channel — scoped by abilityTenantScope the same way route-time channel
// selection is (getChannelQuery/abilityBaseCondition) — can also serve
// them. A disabled sibling channel does not count as a substitute (its
// ability rows are not enabled=true); a sibling that belongs to a
// different tenant does not count either, since abilityTenantScope makes
// it invisible to tenantID's routing in the first place, mirroring how a
// slow channel in one tenant is never "covered" by an unrelated tenant's
// identically-named model.
func SoleEnabledModelsForChannel(channelID int, tenantID string) ([]SoleModel, error) {
	var mine []Ability
	if err := DB.Where("channel_id = ? and enabled = ?", channelID, true).Find(&mine).Error; err != nil {
		return nil, err
	}

	sole := make([]SoleModel, 0, len(mine))
	for _, a := range mine {
		cond, args := abilityBaseCondition(a.Group, a.Model, tenantID)
		cond += " and channel_id <> ?"
		args = append(args, channelID)

		var count int64
		if err := DB.Model(&Ability{}).Where(cond, args...).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			sole = append(sole, SoleModel{Group: a.Group, Model: a.Model})
		}
	}
	return sole, nil
}
