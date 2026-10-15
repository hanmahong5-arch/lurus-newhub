package handler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"

	"github.com/gin-gonic/gin"
)

// Reseller team-asset bundle ("teampack") distribution for white-label
// Switch clients. A reseller signs teampack.tar.gz locally with an ed25519
// key and publishes it through the root admin PUT below; white-label end
// users pull it through the three public GETs.
//
// The Hub NEVER verifies the signature: the verification key (pack_pub_key)
// ships inside the white-label installer and exists only on the terminal, so
// a server-side check would add no trust. The Hub only bounds the size,
// checks the blob is gzip-shaped and the signature is 128 hex characters,
// and computes the sha256 it serves as ETag. Contract:
// doc/coord/contracts.md "newhub -> switch: reseller team pack".
const (
	// maxTeamPackBlobBytes bounds the DECODED tar.gz.
	maxTeamPackBlobBytes = 8 << 20
	// maxTeamPackBodyBytes bounds the raw JSON request body: base64 inflates
	// 3 bytes to 4, so 8 MiB of blob needs ~10.7 MiB; 12 MiB leaves headroom
	// for the JSON envelope without letting a client stream unbounded data.
	maxTeamPackBodyBytes = 12 << 20
	// maxTeamPackVersionLen is the version column width in characters (varchar(64)).
	maxTeamPackVersionLen = 64
	// teamPackSigHexLen is a 64-byte ed25519 signature in hex.
	teamPackSigHexLen = 128

	teamPackTenantNotFoundMsg = "tenant not found"
)

// teamPackErr writes the contract error envelope.
func teamPackErr(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "message": msg})
}

// teamPackVersionValid reports whether v (already trimmed) is a usable
// version label: 1..64 runes, no control characters. This is deliberately the
// same rule the Switch side applies to teampack.yaml's version
// (internal/teampack/validate.go), so a pack that previews and builds there is
// never rejected here on shape alone. Length is counted in runes, matching
// Postgres varchar(64) which also counts characters, not bytes.
func teamPackVersionValid(v string) bool {
	if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxTeamPackVersionLen {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// teamPackMeta is the data object shared by the meta GET and the admin PUT.
func teamPackMeta(p *entity.SwitchTeamPack) gin.H {
	return gin.H{
		"version":    p.Version,
		"sha256":     p.SHA256,
		"size":       p.Size,
		"updated_at": p.UpdatedAt,
	}
}

// loadPublishedTeamPack resolves the :tenant_slug of a public route to its
// published pack. On any miss it writes the 404/500 response itself and
// returns nil. A suspended/disabled tenant is served as "no pack" (404): its
// white-label users must not keep pulling assets from a dead reseller, and
// 404 is the one status the client already treats as "nothing published".
// withBlob=false skips the blob column (meta and .sig never need it).
func loadPublishedTeamPack(c *gin.Context, withBlob bool) *entity.SwitchTeamPack {
	tenant, err := repo.GetTenantBySlug(c.Param("tenant_slug"))
	if err != nil || tenant == nil {
		if err != nil && err.Error() != teamPackTenantNotFoundMsg {
			common.SysError("teampack: tenant lookup failed: " + err.Error())
			teamPackErr(c, http.StatusInternalServerError, "tenant lookup failed")
			return nil
		}
		teamPackErr(c, http.StatusNotFound, "no team pack published")
		return nil
	}
	if ok, _ := repo.TenantGate(tenant.Id); !ok {
		teamPackErr(c, http.StatusNotFound, "no team pack published")
		return nil
	}

	var pack *entity.SwitchTeamPack
	if withBlob {
		pack, err = repo.GetSwitchTeamPack(tenant.Id)
	} else {
		pack, err = repo.GetSwitchTeamPackMeta(tenant.Id)
	}
	if err != nil {
		if errors.Is(err, repo.ErrSwitchTeamPackNotFound) {
			teamPackErr(c, http.StatusNotFound, "no team pack published")
			return nil
		}
		common.SysError("teampack: load failed: " + err.Error())
		teamPackErr(c, http.StatusInternalServerError, "failed to load team pack")
		return nil
	}
	return pack
}

// GetSwitchTeamPackMeta serves the pack's version/sha256/size/updated_at.
// Route: GET /api/v2/:tenant_slug/switch/teampack/meta (public, no auth: the
// pack is signature-verifiable by the terminal and carries no secrets).
func GetSwitchTeamPackMeta(c *gin.Context) {
	pack := loadPublishedTeamPack(c, false)
	if pack == nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": teamPackMeta(pack)})
}

// teamPackETagMatches implements the If-None-Match comparison for a strong
// ETag: "*" or any listed entity-tag (a W/ weak prefix is ignored) equal to
// the quoted sha256.
func teamPackETagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}

// GetSwitchTeamPackBlob serves the raw tar.gz.
// Route: GET /api/v2/:tenant_slug/switch/teampack (public).
func GetSwitchTeamPackBlob(c *gin.Context) {
	pack := loadPublishedTeamPack(c, true)
	if pack == nil {
		return
	}
	etag := `"` + pack.SHA256 + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	if inm := c.GetHeader("If-None-Match"); inm != "" && teamPackETagMatches(inm, etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Content-Length", strconv.Itoa(len(pack.Blob)))
	c.Data(http.StatusOK, "application/gzip", pack.Blob)
}

// GetSwitchTeamPackSig serves the detached signature as 128 hex characters,
// no trailing newline.
// Route: GET /api/v2/:tenant_slug/switch/teampack.sig (public). The path is
// the pack address plus ".sig" - the suffix convention the Switch client's
// source.sigURL already uses, so it cannot change.
func GetSwitchTeamPackSig(c *gin.Context) {
	pack := loadPublishedTeamPack(c, false)
	if pack == nil {
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(pack.Sig))
}

// resolveTeamPackAdminTenant maps the admin route's :id (tenant id first,
// then slug) to a tenant row. "default" is refused with 400: it is the shared
// platform tenant, not a reseller. Writes the response itself on failure and
// returns nil.
func resolveTeamPackAdminTenant(c *gin.Context) *repo.Tenant {
	ref := strings.TrimSpace(c.Param("id"))
	if ref == "" || ref == "default" {
		teamPackErr(c, http.StatusBadRequest, "the default tenant cannot publish a team pack")
		return nil
	}
	tenant, err := repo.GetTenantByID(ref)
	if err != nil {
		if err.Error() != teamPackTenantNotFoundMsg {
			common.SysError("teampack: tenant lookup failed: " + err.Error())
			teamPackErr(c, http.StatusInternalServerError, "tenant lookup failed")
			return nil
		}
		tenant, err = repo.GetTenantBySlug(ref)
		if err != nil {
			if err.Error() != teamPackTenantNotFoundMsg {
				common.SysError("teampack: tenant lookup failed: " + err.Error())
				teamPackErr(c, http.StatusInternalServerError, "tenant lookup failed")
				return nil
			}
			teamPackErr(c, http.StatusNotFound, teamPackTenantNotFoundMsg)
			return nil
		}
	}
	if tenant == nil {
		teamPackErr(c, http.StatusNotFound, teamPackTenantNotFoundMsg)
		return nil
	}
	if tenant.Id == "default" || tenant.Slug == "default" {
		teamPackErr(c, http.StatusBadRequest, "the default tenant cannot publish a team pack")
		return nil
	}
	return tenant
}

// PutSwitchTeamPack publishes (or replaces) the tenant's signed team pack.
// Route: PUT /api/v2/admin/tenants/:id/switch-teampack (RootJWTAuth). The
// signature is stored verbatim and NOT verified here - see the file header.
func PutSwitchTeamPack(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTeamPackBodyBytes)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		teamPackErr(c, http.StatusBadRequest, "request body too large or unreadable (max 12 MiB)")
		return
	}

	var req struct {
		Version    string `json:"version"`
		BlobBase64 string `json:"blob_base64"`
		SigHex     string `json:"sig_hex"`
	}
	if err := common.Unmarshal(raw, &req); err != nil {
		teamPackErr(c, http.StatusBadRequest, "invalid request body")
		return
	}

	version := strings.TrimSpace(req.Version)
	if !teamPackVersionValid(version) {
		teamPackErr(c, http.StatusBadRequest, "version must be 1-64 characters with no control characters")
		return
	}
	sigHex := strings.TrimSpace(req.SigHex)
	if len(sigHex) != teamPackSigHexLen {
		teamPackErr(c, http.StatusBadRequest, "sig_hex must be exactly 128 hex characters")
		return
	}
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		teamPackErr(c, http.StatusBadRequest, "sig_hex must be exactly 128 hex characters")
		return
	}
	blob, err := base64.StdEncoding.DecodeString(req.BlobBase64)
	if err != nil {
		teamPackErr(c, http.StatusBadRequest, "blob_base64 is not valid standard base64")
		return
	}
	if len(blob) > maxTeamPackBlobBytes {
		teamPackErr(c, http.StatusBadRequest, "team pack exceeds the 8 MiB limit")
		return
	}
	// gzip magic only: the tarball inside is the terminal's to parse and
	// verify; the Hub just refuses obviously wrong payloads.
	if len(blob) < 2 || blob[0] != 0x1f || blob[1] != 0x8b {
		teamPackErr(c, http.StatusBadRequest, "blob must be a gzip stream (tar.gz)")
		return
	}

	tenant := resolveTeamPackAdminTenant(c)
	if tenant == nil {
		return
	}

	sum := sha256.Sum256(blob)
	pack := &entity.SwitchTeamPack{
		TenantID:  tenant.Id,
		Version:   version,
		SHA256:    hex.EncodeToString(sum[:]),
		Size:      int64(len(blob)),
		Blob:      blob,
		Sig:       hex.EncodeToString(sigBytes), // normalised to lower case
		UpdatedAt: common.GetTimestamp(),
	}
	if err := repo.UpsertSwitchTeamPack(pack); err != nil {
		common.SysError("teampack: save failed: " + err.Error())
		teamPackErr(c, http.StatusInternalServerError, "failed to save team pack")
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0,
		"switch-teampack set: "+tenant.Id+" version="+version+" sha256="+pack.SHA256))

	c.JSON(http.StatusOK, gin.H{"success": true, "data": teamPackMeta(pack)})
}

// DeleteSwitchTeamPack withdraws the tenant's team pack. Idempotent: 200
// whether or not a pack existed.
// Route: DELETE /api/v2/admin/tenants/:id/switch-teampack (RootJWTAuth).
func DeleteSwitchTeamPack(c *gin.Context) {
	tenant := resolveTeamPackAdminTenant(c)
	if tenant == nil {
		return
	}
	if err := repo.DeleteSwitchTeamPack(tenant.Id); err != nil {
		common.SysError("teampack: delete failed: " + err.Error())
		teamPackErr(c, http.StatusInternalServerError, "failed to delete team pack")
		return
	}

	actorID, _ := repo.GetUserID(c)
	governance.RecordAuditEvent(governance.NewAuditEvent(c, governance.ActorAdmin, actorID,
		governance.ActionTenantUpdated, governance.ResourceTenant, 0,
		"switch-teampack deleted: "+tenant.Id))

	c.JSON(http.StatusOK, gin.H{"success": true})
}
