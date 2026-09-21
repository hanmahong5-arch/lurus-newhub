package repo

// user_edit.go — (*User).Edit, the admin update path's write. Split out of
// user.go by the cycle-13 wiring pass as a pure move: the method is
// byte-identical to the one that stood in user.go. internal/pkg/gates'
// source-size ratchet holds user.go at its measured line count, so this
// cycle's additions to that file — the daily_quota / base_group /
// fallback_group columns L7 added to the update map — are paid for by a move
// rather than by raising the ceiling.

func (user *User) Edit() error {
	newUser := *user
	updates := map[string]interface{}{
		"username":     newUser.Username,
		"display_name": newUser.DisplayName,
		"group":        newUser.Group,
		"quota":        newUser.Quota,
		"remark":       newUser.Remark,
	}
	// Edit is only reachable from the admin update path (handler.UpdateUser), which
	// has already enforced CheckPermission/CheckRolePromotion; the self-service path
	// (handler.UpdateSelf) goes through Update() with a clean struct and can never
	// reach here. Zero means "not provided" in the decoded JSON payload (valid
	// values start at 1, see common.UserStatusEnabled), so skip it to avoid
	// resetting role/status on partial updates.
	if newUser.Role != 0 {
		updates["role"] = newUser.Role
	}
	if newUser.Status != 0 {
		updates["status"] = newUser.Status
	}
	// cycle-13 L7: these three were absent from the map entirely, so
	// EditUserModal.jsx's daily-quota/base-group/fallback-group fields
	// submitted and the handler answered success while nothing was ever
	// written. Same zero-means-not-provided convention as role/status
	// above, for the same underlying reason: Edit's only caller
	// (handler.UpdateUser) decodes the raw JSON body straight into this
	// plain int/string struct, so by the time newUser reaches this method
	// "0"/"" already cannot be told apart from "the field was omitted from
	// the request" — that decode happens outside this package. Unlike
	// role/status, 0 and "" ARE legitimate values here (0 daily_quota means
	// unlimited; "" base_group/fallback_group means no override), so a
	// caller that means to explicitly CLEAR one of these three back to its
	// zero value cannot do it through this endpoint today — that needs a
	// pointer-typed request DTO or an explicit Select() column list at the
	// handler, which is outside this file's ownership. Tracked as a known
	// gap rather than silently mis-clearing on every partial update.
	if newUser.DailyQuota != 0 {
		updates["daily_quota"] = newUser.DailyQuota
	}
	if newUser.BaseGroup != "" {
		updates["base_group"] = newUser.BaseGroup
	}
	if newUser.FallbackGroup != "" {
		updates["fallback_group"] = newUser.FallbackGroup
	}

	DB.First(&user, user.Id)
	if err := DB.Model(user).Updates(updates).Error; err != nil {
		return err
	}
	return updateUserCache(*user)
}
