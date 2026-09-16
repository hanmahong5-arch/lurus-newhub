package repo

// chat_session.go — persistence for chat_sessions + chat_messages
// (migration 038, cycle-10 L3): a client-driven, best-effort SAVE of a v2
// console Chat conversation. See entity.ChatSession's doc comment for the
// relationship (or lack of one) to ChatSend's actual completion call.

import (
	"errors"
	"fmt"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"gorm.io/gorm"
)

// ErrChatSessionNotFound covers BOTH "no row with this id" and "row exists
// but belongs to a different tenant/user" — every lookup in this file
// filters by (id, tenant_id, user_id) in a single WHERE clause (or, for
// DeleteChatSessionOwned, a single DELETE with the same WHERE), so the two
// cases produce the identical gorm.ErrRecordNotFound / RowsAffected==0 and
// therefore the identical mapped error. Callers (v2_chat_session.go) must
// not try to tell the two apart — there is no second branch here that
// could accidentally do so. Same convention as ErrGrantNotFound
// (admin_permission_grant.go) and task_artifacts.go's loadOwnedTask.
var ErrChatSessionNotFound = errors.New("chat session not found")

// ChatMessageInput is the turn shape a caller of Create/Update supplies —
// deliberately narrower than entity.ChatMessage (no Id/SessionId/TenantId/
// UserId/CreatedAt/Seq): filling those in is this file's job, not the
// handler's.
type ChatMessageInput struct {
	Role    string
	Content string
}

// ChatSessionSummary is one row of ListChatSessions: a session plus the
// message count the sidebar needs to render "N turns" without a second
// per-row round trip.
type ChatSessionSummary struct {
	entity.ChatSession
	MessageCount int64 `json:"message_count"`
}

// CreateChatSession inserts a session row plus its initial turns (msgs may
// be empty — an empty conversation is a legal, if unusual, save) in one
// transaction. CreatedAt/UpdatedAt are left to GORM's standard
// convention-based auto-population (matches repo.CreateProject).
func CreateChatSession(tenantID string, userID int, title, model string, msgs []ChatMessageInput) (*entity.ChatSession, error) {
	if tenantID == "" || userID <= 0 {
		return nil, errors.New("tenant id and user id are required")
	}
	session := entity.ChatSession{
		TenantId: tenantID,
		UserId:   userID,
		Title:    title,
		Model:    model,
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&session).Error; err != nil {
			return fmt.Errorf("create chat session: %w", err)
		}
		if len(msgs) == 0 {
			return nil
		}
		rows := make([]entity.ChatMessage, len(msgs))
		for i, m := range msgs {
			rows[i] = entity.ChatMessage{
				SessionId: session.Id,
				TenantId:  tenantID,
				UserId:    userID,
				Seq:       i,
				Role:      m.Role,
				Content:   m.Content,
			}
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("create chat messages: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// ListChatSessions returns the caller's own sessions, most-recently-updated
// first.
func ListChatSessions(tenantID string, userID int) ([]ChatSessionSummary, error) {
	if tenantID == "" || userID <= 0 {
		return nil, errors.New("tenant id and user id are required")
	}
	var sessions []entity.ChatSession
	if err := DB.Where("tenant_id = ? AND user_id = ?", tenantID, userID).
		Order("updated_at DESC").Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("list chat sessions: %w", err)
	}
	out := make([]ChatSessionSummary, len(sessions))
	if len(sessions) == 0 {
		return out, nil
	}
	ids := make([]int, len(sessions))
	for i, s := range sessions {
		ids[i] = s.Id
	}
	var counts []struct {
		SessionId int
		Count     int64
	}
	if err := DB.Model(&entity.ChatMessage{}).
		Select("session_id, COUNT(*) as count").
		Where("session_id IN ?", ids).
		Group("session_id").
		Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("count chat messages: %w", err)
	}
	countBySession := make(map[int]int64, len(counts))
	for _, c := range counts {
		countBySession[c.SessionId] = c.Count
	}
	for i, s := range sessions {
		out[i] = ChatSessionSummary{ChatSession: s, MessageCount: countBySession[s.Id]}
	}
	return out, nil
}

// GetChatSessionOwned fetches one session (see ErrChatSessionNotFound) plus
// its messages in Seq order.
func GetChatSessionOwned(tenantID string, userID, id int) (*entity.ChatSession, []entity.ChatMessage, error) {
	if tenantID == "" || userID <= 0 || id <= 0 {
		return nil, nil, ErrChatSessionNotFound
	}
	var session entity.ChatSession
	if err := DB.Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).
		First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrChatSessionNotFound
		}
		return nil, nil, fmt.Errorf("get chat session: %w", err)
	}
	var messages []entity.ChatMessage
	if err := DB.Where("session_id = ?", session.Id).Order("seq ASC").Find(&messages).Error; err != nil {
		return nil, nil, fmt.Errorf("list chat messages: %w", err)
	}
	return &session, messages, nil
}

// UpdateChatSessionOwned applies a partial update to a session the caller
// owns: title (when non-nil) and/or a full REPLACEMENT of its messages
// (when msgs is non-nil — an empty, non-nil slice clears history, mirroring
// the front-end PATCHing the complete client-side array it already holds,
// not a diff). Both the title write and the delete-then-reinsert of
// messages happen in one transaction, so a mid-write failure cannot leave
// the session with half its former history and half its new history.
func UpdateChatSessionOwned(tenantID string, userID, id int, title *string, msgs []ChatMessageInput) (*entity.ChatSession, error) {
	if tenantID == "" || userID <= 0 || id <= 0 {
		return nil, ErrChatSessionNotFound
	}
	var session entity.ChatSession
	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).First(&session)
		if res.Error != nil {
			if errors.Is(res.Error, gorm.ErrRecordNotFound) {
				return ErrChatSessionNotFound
			}
			return res.Error
		}
		if msgs != nil {
			if err := tx.Where("session_id = ?", session.Id).Delete(&entity.ChatMessage{}).Error; err != nil {
				return fmt.Errorf("clear chat messages: %w", err)
			}
			if len(msgs) > 0 {
				rows := make([]entity.ChatMessage, len(msgs))
				for i, m := range msgs {
					rows[i] = entity.ChatMessage{
						SessionId: session.Id,
						TenantId:  tenantID,
						UserId:    userID,
						Seq:       i,
						Role:      m.Role,
						Content:   m.Content,
					}
				}
				if err := tx.Create(&rows).Error; err != nil {
					return fmt.Errorf("replace chat messages: %w", err)
				}
			}
		}
		if title == nil && msgs == nil {
			// Nothing to change — do not touch UpdatedAt for a no-op call.
			return nil
		}
		updates := map[string]any{"updated_at": time.Now()}
		if title != nil {
			updates["title"] = *title
			session.Title = *title
		}
		if err := tx.Model(&entity.ChatSession{}).Where("id = ?", session.Id).Updates(updates).Error; err != nil {
			return fmt.Errorf("update chat session: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// DeleteChatSessionOwned removes a session and its messages in one
// transaction. Same ErrChatSessionNotFound as GetChatSessionOwned for
// "absent" and "not owned" — the ownership check IS the DELETE's WHERE
// clause (RowsAffected == 0 means the same thing whichever reason caused
// it), so there is no separate existence check that could fall out of sync
// with the actual delete.
func DeleteChatSessionOwned(tenantID string, userID, id int) error {
	if tenantID == "" || userID <= 0 || id <= 0 {
		return ErrChatSessionNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND tenant_id = ? AND user_id = ?", id, tenantID, userID).
			Delete(&entity.ChatSession{})
		if res.Error != nil {
			return fmt.Errorf("delete chat session: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrChatSessionNotFound
		}
		if err := tx.Where("session_id = ?", id).Delete(&entity.ChatMessage{}).Error; err != nil {
			return fmt.Errorf("delete chat messages: %w", err)
		}
		return nil
	})
}
