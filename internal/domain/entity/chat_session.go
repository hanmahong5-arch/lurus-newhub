package entity

import "time"

// ChatSession is one persisted multi-turn conversation created through the
// v2 console's Chat page (GET/POST/PATCH/DELETE
// /api/v2/:tenant_slug/chat/sessions[/:id], internal/adapter/handler/
// v2_chat_session.go). It is unrelated to ResponseRegistry (which pins a
// vendor-minted Responses-API id to a channel) and to the completion call
// itself: ChatSend (handler/v2_chat.go) still runs each turn via
// self-HTTP loopback to /v1/chat/completions and neither reads nor writes
// this table — a session row is the CLIENT saving a conversation ChatSend
// already returned, not a server-side conversation store ChatSend consults
// on its hot path. Persistence here is therefore best-effort exactly the
// way ResponseRegistry's insert is: a save failure must not be allowed to
// undo an already-rendered chat turn.
//
// Schema is created by migration 038 (chat_sessions) alone — see that
// file's header for why it, not AutoMigrate, is the sole creator today.
type ChatSession struct {
	Id        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantId  string    `json:"tenant_id" gorm:"type:varchar(36);not null;index:idx_chat_sessions_tenant_user,priority:1"`
	UserId    int       `json:"user_id" gorm:"not null;index:idx_chat_sessions_tenant_user,priority:2"`
	Title     string    `json:"title" gorm:"type:varchar(255);not null;default:''"`
	Model     string    `json:"model" gorm:"type:varchar(128);not null;default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (ChatSession) TableName() string {
	return "chat_sessions"
}

// ChatMessage is one turn of a ChatSession, ordered by Seq within the
// session — NOT by CreatedAt: repo.UpdateChatSessionOwned replaces a
// session's entire message set in one transaction (a PATCH ships the
// client's full array, not a diff), so every row in a given replacement
// can legitimately share the same timestamp; Seq is what preserves turn
// order regardless of timestamp resolution.
type ChatMessage struct {
	Id        int       `json:"id" gorm:"primaryKey;autoIncrement"`
	SessionId int       `json:"session_id" gorm:"not null;index:idx_chat_messages_session,priority:1"`
	TenantId  string    `json:"tenant_id" gorm:"type:varchar(36);not null"`
	UserId    int       `json:"user_id" gorm:"not null"`
	Seq       int       `json:"seq" gorm:"not null;index:idx_chat_messages_session,priority:2"`
	Role      string    `json:"role" gorm:"type:varchar(16);not null"`
	Content   string    `json:"content" gorm:"type:text;not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
}

// TableName overrides the default GORM table name.
func (ChatMessage) TableName() string {
	return "chat_messages"
}
