package entity

type Midjourney struct {
	Id          int    `json:"id"`
	Code        int    `json:"code"`
	UserId      int    `json:"user_id" gorm:"index"`
	Action      string `json:"action" gorm:"type:varchar(40);index"`
	MjId        string `json:"mj_id" gorm:"index"`
	Prompt      string `json:"prompt"`
	PromptEn    string `json:"prompt_en"`
	Description string `json:"description"`
	State       string `json:"state"`
	SubmitTime  int64  `json:"submit_time" gorm:"index"`
	StartTime   int64  `json:"start_time" gorm:"index"`
	FinishTime  int64  `json:"finish_time" gorm:"index"`
	ImageUrl    string `json:"image_url"`
	VideoUrl    string `json:"video_url"`
	VideoUrls   string `json:"video_urls"`
	Status      string `json:"status" gorm:"type:varchar(20);index"`
	Progress    string `json:"progress" gorm:"type:varchar(30);index"`
	FailReason  string `json:"fail_reason"`
	ChannelId   int    `json:"channel_id"`
	Quota       int    `json:"quota"`
	Buttons     string `json:"buttons"`
	Properties  string `json:"properties"`
}

type TaskQueryParams struct {
	ChannelID      string
	MjID           string
	StartTimestamp string
	EndTimestamp   string
	// TenantID scopes the admin list (repo.GetAllTasks/CountAllTasks) to one
	// tenant's Midjourney rows via a subquery on the owning user (cycle-11
	// L8). The caller (GetAllMidjourney) sets both TenantID and
	// TenantScoped for every non-root admin, including one whose session
	// has no tenant yet (TenantID == "" — see TenantScoped's fail-closed
	// note); root leaves TenantScoped false and keeps the platform-wide view.
	TenantID string
	// TenantScoped, when true, makes the repo apply the tenant subquery even
	// if TenantID is "" — an empty-string tenant then matches no user row,
	// so the caller gets an empty page rather than every tenant's rows.
	// Fail-closed by construction: this is the flag, not TenantID != "",
	// that gates the WHERE clause (operator decision, cycle-11 L8 repair —
	// mirrors GetAllChannels' `if !isRoot { WHERE tenant_id = callerTenant }`,
	// which is unconditional on emptiness the same way).
	TenantScoped bool
}
