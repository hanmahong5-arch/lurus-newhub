package repo

// log_internal_queries.go - the internal-API log pagers, moved verbatim out
// of log.go (pure move, cycle 22) so that file stays under its source-size
// ceiling.

// GetUserLogsInternal returns paginated logs for a user (internal API, no tenant filter).
func GetUserLogsInternal(userID, offset, limit int) (logs []*Log, total int64, err error) {
	tx := LOG_DB.Model(&Log{}).Where("user_id = ?", userID)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}

// GetTokenLogsInternal returns paginated logs filtered by token ID (internal API).
func GetTokenLogsInternal(tokenID, offset, limit int) (logs []*Log, total int64, err error) {
	tx := LOG_DB.Model(&Log{}).Where("token_id = ?", tokenID)
	err = tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("created_at DESC").Offset(offset).Limit(limit).Find(&logs).Error
	return logs, total, err
}
