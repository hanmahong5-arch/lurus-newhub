package repo

// ListTenantChannelsWithKeys loads the full rows (key column included) of one
// tenant's channels, optionally narrowed to ids. The keys are needed only to
// COUNT them for the ops views (multi-key channels store them newline
// separated in one column); callers must never serialise Key.
func ListTenantChannelsWithKeys(tenantID string, ids []int, limit int) ([]*Channel, error) {
	var channels []*Channel
	q := DB.Where("tenant_id = ?", tenantID)
	if ids != nil {
		if len(ids) == 0 {
			return channels, nil
		}
		q = q.Where("id IN ?", ids)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Order("id desc").Find(&channels).Error
	return channels, err
}

// ListChannelTemplateApplications returns the application log of one template,
// newest first, plus the total row count.
func ListChannelTemplateApplications(templateID int64, offset, limit int) ([]ChannelTemplateApplication, int64, error) {
	var total int64
	q := DB.Model(&ChannelTemplateApplication{}).Where("template_id = ?", templateID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []ChannelTemplateApplication{}
	err := DB.Where("template_id = ?", templateID).Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}
