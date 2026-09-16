package handler

// v2_chat_session.go — GET/POST/PATCH/DELETE
// /api/v2/:tenant_slug/chat/sessions[/:id] (cycle-10 L3, migration 038).
// This is the client-driven SAVE of a conversation ChatSend (v2_chat.go)
// already ran and returned; it does not itself call any model. All five
// handlers here read the caller's identity from
// middleware.GetTenantContext(c) (TenantSlugGuard has already resolved and
// matched the URL's :tenant_slug by the time these run — same convention
// as GetTopUpsV2/ListInvoicesV2 in v2_billing.go), never from the URL, so a
// caller cannot address another tenant's rows by editing the id alone.
//
// OWNERSHIP: every repo function this file calls (GetChatSessionOwned/
// UpdateChatSessionOwned/DeleteChatSessionOwned) answers
// repo.ErrChatSessionNotFound identically for "no such id" and "id belongs
// to someone else" — respondChatSessionNotFound below is the single place
// that maps it to the wire response, so the two cases are byte-identical
// on every route in this file. See repo/chat_session.go's doc comment for
// why the query shape itself, not a second branch, is what guarantees this.
import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/LurusTech/lurus-hub/internal/adapter/middleware"
	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"

	"github.com/gin-gonic/gin"
)

// maxChatSessionTitleRunes matches chat_sessions.title's VARCHAR(255)
// (migration 038) — enforced here so an oversized title fails with a 400
// a caller can act on instead of a database error. Rejected, not
// truncated: silently storing a different string than the caller sent
// would leave the caller believing its own title was saved verbatim.
const maxChatSessionTitleRunes = 255

// maxChatMessageContentRunes matches the v2 Chat page's own input counter
// (web/src/pages/v2/Chat/index.jsx's "{{count}} / 200,000"): the backend
// enforces the same ceiling the UI already advertises rather than silently
// accepting more than the UI tells a user is allowed.
const maxChatMessageContentRunes = 200_000

// chatSessionAllowedRoles are the only Role values a caller may persist —
// the same three roles ChatSend's own request/response shapes use
// (playgroundChatMessage's role, and the "assistant" ChatSend always
// answers with).
var chatSessionAllowedRoles = map[string]bool{"user": true, "assistant": true, "system": true}

func requireChatTenantContext(c *gin.Context) (*middleware.TenantContext, bool) {
	tenantCtx, err := middleware.GetTenantContext(c)
	if err != nil || tenantCtx == nil || tenantCtx.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":    false,
			"message":    "Not authenticated",
			"error_code": "UNAUTHENTICATED",
		})
		return nil, false
	}
	return tenantCtx, true
}

func respondChatSessionNotFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{
		"success":    false,
		"message":    "Chat session not found",
		"error_code": "CHAT_SESSION_NOT_FOUND",
	})
}

type chatSessionMessageInput struct {
	Role    string `json:"role" binding:"required"`
	Content string `json:"content"`
}

// parseChatSessionMessages validates and converts the wire shape into
// repo.ChatMessageInput. A nil input slice is passed through as nil (the
// "field absent" vs "field present but empty" distinction PATCH needs to
// tell "leave messages alone" apart from "clear them" — see
// repo.UpdateChatSessionOwned's doc comment); an oversized or unrecognised
// role/content is rejected with an error the caller renders as 400, never
// silently dropped or truncated.
func parseChatSessionMessages(in []chatSessionMessageInput) ([]repo.ChatMessageInput, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]repo.ChatMessageInput, len(in))
	for i, m := range in {
		if !chatSessionAllowedRoles[m.Role] {
			return nil, errors.New("messages[" + strconv.Itoa(i) + "].role must be one of user, assistant, system")
		}
		if len([]rune(m.Content)) > maxChatMessageContentRunes {
			return nil, errors.New("messages[" + strconv.Itoa(i) + "].content exceeds " + strconv.Itoa(maxChatMessageContentRunes) + " characters")
		}
		out[i] = repo.ChatMessageInput{Role: m.Role, Content: m.Content}
	}
	return out, nil
}

// validateChatSessionTitle trims and rejects (never truncates — see
// maxChatSessionTitleRunes's doc comment) a title over the column width.
func validateChatSessionTitle(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len([]rune(trimmed)) > maxChatSessionTitleRunes {
		return "", errors.New("title exceeds " + strconv.Itoa(maxChatSessionTitleRunes) + " characters")
	}
	return trimmed, nil
}

func chatSessionSummaryJSON(s repo.ChatSessionSummary) gin.H {
	return gin.H{
		"id":            s.Id,
		"title":         s.Title,
		"model":         s.Model,
		"message_count": s.MessageCount,
		"created_at":    s.CreatedAt,
		"updated_at":    s.UpdatedAt,
	}
}

func chatSessionDetailJSON(session *entity.ChatSession, messages []entity.ChatMessage) gin.H {
	msgs := make([]gin.H, len(messages))
	for i, m := range messages {
		msgs[i] = gin.H{"role": m.Role, "content": m.Content}
	}
	return gin.H{
		"id":         session.Id,
		"title":      session.Title,
		"model":      session.Model,
		"created_at": session.CreatedAt,
		"updated_at": session.UpdatedAt,
		"messages":   msgs,
	}
}

// ListChatSessionsV2 serves GET /api/v2/:tenant_slug/chat/sessions — the
// v2 Chat page's sidebar.
func ListChatSessionsV2(c *gin.Context) {
	tenantCtx, ok := requireChatTenantContext(c)
	if !ok {
		return
	}
	sessions, err := repo.ListChatSessions(tenantCtx.TenantID, tenantCtx.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to list chat sessions",
			"error_code": "CHAT_SESSION_LIST_FAILED",
		})
		return
	}
	rows := make([]gin.H, len(sessions))
	for i, s := range sessions {
		rows[i] = chatSessionSummaryJSON(s)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"sessions": rows}})
}

type createChatSessionRequest struct {
	Title    string                    `json:"title"`
	Model    string                    `json:"model"`
	Messages []chatSessionMessageInput `json:"messages"`
}

// CreateChatSessionV2 serves POST /api/v2/:tenant_slug/chat/sessions — a
// SAVE of a conversation the client already holds (see this file's header
// comment): the request carries the full turn array the same way
// ChatSend's own request does, not a single new turn to append.
func CreateChatSessionV2(c *gin.Context) {
	tenantCtx, ok := requireChatTenantContext(c)
	if !ok {
		return
	}
	var req createChatSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid request: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	title, err := validateChatSessionTitle(req.Title)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	msgs, err := parseChatSessionMessages(req.Messages)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	session, err := repo.CreateChatSession(tenantCtx.TenantID, tenantCtx.UserID, title, req.Model, msgs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to create chat session",
			"error_code": "CHAT_SESSION_CREATE_FAILED",
		})
		return
	}
	entityMsgs := make([]entity.ChatMessage, len(msgs))
	for i, m := range msgs {
		entityMsgs[i] = entity.ChatMessage{Role: m.Role, Content: m.Content}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": chatSessionDetailJSON(session, entityMsgs)})
}

func parseChatSessionID(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		respondChatSessionNotFound(c)
		return 0, false
	}
	return id, true
}

// GetChatSessionV2 serves GET /api/v2/:tenant_slug/chat/sessions/:id —
// rehydrates one conversation (the sidebar's click-to-open).
func GetChatSessionV2(c *gin.Context) {
	tenantCtx, ok := requireChatTenantContext(c)
	if !ok {
		return
	}
	id, ok := parseChatSessionID(c)
	if !ok {
		return
	}
	session, messages, err := repo.GetChatSessionOwned(tenantCtx.TenantID, tenantCtx.UserID, id)
	if err != nil {
		if errors.Is(err, repo.ErrChatSessionNotFound) {
			respondChatSessionNotFound(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to load chat session",
			"error_code": "CHAT_SESSION_LOAD_FAILED",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": chatSessionDetailJSON(session, messages)})
}

type updateChatSessionRequest struct {
	Title *string `json:"title"`
	// Messages uses a pointer-to-slice so ShouldBindJSON can tell "field
	// omitted" (nil pointer — title-only update, existing messages
	// untouched) apart from "field present as []" (non-nil pointer to an
	// empty slice — explicitly clear the conversation) apart from "field
	// present with turns" (non-nil pointer to a populated slice — replace).
	// A plain (non-pointer) slice field cannot make the first two cases
	// distinguishable: encoding/json leaves an omitted slice field at its
	// nil zero value, which is indistinguishable from an explicit `[]`
	// decoded the same way once unmarshalled into a bare slice.
	Messages *[]chatSessionMessageInput `json:"messages"`
}

// UpdateChatSessionV2 serves PATCH /api/v2/:tenant_slug/chat/sessions/:id.
// Messages, when the field is present at all (even as []), REPLACES the
// stored turn list — see repo.UpdateChatSessionOwned's doc comment for why
// that is a replace and not an append.
func UpdateChatSessionV2(c *gin.Context) {
	tenantCtx, ok := requireChatTenantContext(c)
	if !ok {
		return
	}
	id, ok := parseChatSessionID(c)
	if !ok {
		return
	}
	var req updateChatSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":    false,
			"message":    "Invalid request: " + err.Error(),
			"error_code": "INVALID_REQUEST",
		})
		return
	}
	if req.Title != nil {
		trimmed, err := validateChatSessionTitle(*req.Title)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    err.Error(),
				"error_code": "INVALID_REQUEST",
			})
			return
		}
		req.Title = &trimmed
	}
	var msgs []repo.ChatMessageInput
	if req.Messages != nil {
		parsed, err := parseChatSessionMessages(*req.Messages)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success":    false,
				"message":    err.Error(),
				"error_code": "INVALID_REQUEST",
			})
			return
		}
		if parsed == nil {
			parsed = []repo.ChatMessageInput{}
		}
		msgs = parsed
	}
	session, err := repo.UpdateChatSessionOwned(tenantCtx.TenantID, tenantCtx.UserID, id, req.Title, msgs)
	if err != nil {
		if errors.Is(err, repo.ErrChatSessionNotFound) {
			respondChatSessionNotFound(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to update chat session",
			"error_code": "CHAT_SESSION_UPDATE_FAILED",
		})
		return
	}
	// Re-read so the response always reflects the persisted row AND
	// messages, whether or not this call touched them — `session` above is
	// the pre-update struct UpdateChatSessionOwned only mutated the Title
	// field of in-memory (its UpdatedAt was set on the row by a raw
	// tx.Model(...).Updates map, never copied back onto that struct), so
	// using it here would answer a PATCH response whose updated_at
	// predates the write this handler just performed.
	fresh, messages, err := repo.GetChatSessionOwned(tenantCtx.TenantID, tenantCtx.UserID, session.Id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to reload chat session",
			"error_code": "CHAT_SESSION_LOAD_FAILED",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": chatSessionDetailJSON(fresh, messages)})
}

// DeleteChatSessionV2 serves DELETE /api/v2/:tenant_slug/chat/sessions/:id.
func DeleteChatSessionV2(c *gin.Context) {
	tenantCtx, ok := requireChatTenantContext(c)
	if !ok {
		return
	}
	id, ok := parseChatSessionID(c)
	if !ok {
		return
	}
	if err := repo.DeleteChatSessionOwned(tenantCtx.TenantID, tenantCtx.UserID, id); err != nil {
		if errors.Is(err, repo.ErrChatSessionNotFound) {
			respondChatSessionNotFound(c)
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":    false,
			"message":    "Failed to delete chat session",
			"error_code": "CHAT_SESSION_DELETE_FAILED",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": nil})
}
