package entity

// ResponseRegistry pins a POST /v1/responses id (OpenAI Responses API,
// created with store != false) to the exact channel + upstream model that
// produced it, so a later GET/DELETE /v1/responses/:response_id
// (handler.RelayResponsesRetrieve / RelayResponsesDelete) can route straight
// back to that channel instead of going through weighted channel selection —
// an id minted by one vendor is meaningless to a different one.
//
// Written by relay.ResponsesHelper's post-postConsumeQuota insert hook
// (internal/app/relay/responses_handler.go) for a successful, non-compact
// POST /v1/responses whose channel type is in
// common.SupportsResponsesStateful's allow-list (a SEPARATE predicate from
// the /v1/responses/compact gate, common.SupportsResponsesCompact — the two
// questions are not the same, even though both happen to be OpenAI-only
// this cycle), whose channel is NOT multi-key (a multi-key channel picks a
// key per request, so pinning only channel_id — not a key index migration
// 036 has no column for — would replay the retrieval on the wrong key;
// cycle-8 L7 repair round, finding B-F3), whose request did not explicitly
// opt out with store:false, AND for which the vendor response carried an id
// that OaiResponsesHandler/OaiResponsesStreamHandler actually stashed
// (c.Set("responses_id", ...) — an id-less vendor body writes no row). That
// insert is best-effort: a registry write failure is logged and counted
// (metrics.ResponseRegistryErrorsTotal) but never fails the billed response.
//
// ResponseId is the vendor-minted id itself (e.g. OpenAI's "resp_..."
// strings) and is the primary key directly — insert/get/delete key off it
// directly (repo.UpsertResponseRegistry/GetResponseRegistry/
// DeleteResponseRegistry); the retention sweep instead scans by ExpiresAt
// (its own index) and only reaches ResponseId to build the batched DELETE
// (repo.SweepExpiredResponseRegistry). A vendor id is already globally
// unique within that vendor's namespace.
//
// Schema is managed by migration 036 (response_registry) plus AutoMigrate
// (repo.migrateDB) for the table + both indexes — unlike 034's
// admin_permission_grants, there is no partial/composite index GORM cannot
// express, so both creation paths converge on identical schema and neither
// is authoritative over the other (032/033 dual-creation pattern).
type ResponseRegistry struct {
	ResponseId    string `json:"response_id" gorm:"primaryKey;type:varchar(128)"`
	TenantId      string `json:"tenant_id" gorm:"type:varchar(36);not null;index:idx_response_registry_tenant_user,priority:1"`
	UserId        int    `json:"user_id" gorm:"not null;index:idx_response_registry_tenant_user,priority:2"`
	TokenId       int    `json:"token_id" gorm:"not null"`
	ChannelId     int    `json:"channel_id" gorm:"not null"`
	UpstreamModel string `json:"upstream_model" gorm:"type:varchar(128);not null"`
	CreatedAt     int64  `json:"created_at" gorm:"not null"`
	ExpiresAt     int64  `json:"expires_at" gorm:"not null;index:idx_response_registry_expires_at"`
}

// TableName overrides the default GORM table name.
func (ResponseRegistry) TableName() string {
	return "response_registry"
}
