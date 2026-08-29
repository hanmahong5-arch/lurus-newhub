package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/pkg/common"
)

const secondsPerDay int64 = 24 * 60 * 60

// rotatedKeyPrefixLen is how many leading characters of a rotated key are kept
// in the audit trail to identify it. A short prefix of a 48-char key is enough
// to correlate the rotation without storing the secret.
const rotatedKeyPrefixLen = 8

// EmailSender sends a notification email. It is injected into RotateDueTokens
// so the rotation logic can be tested without a live SMTP server; production
// passes common.SendEmail.
type EmailSender func(subject, receiver, content string) error

// NeedsRotation reports whether a token configured to auto-rotate every
// autoRotateDays days, last rotated (or created) at rotatedAt, is due now.
func NeedsRotation(autoRotateDays int, rotatedAt int64) bool {
	return NeedsRotationAt(autoRotateDays, rotatedAt, common.GetTimestamp())
}

// NeedsRotationAt is the pure, clock-injectable core of NeedsRotation: it
// reports whether a token whose rotation interval is autoRotateDays days, with
// rotation baseline rotatedAt, is due for rotation as of now. All times are
// unix seconds; autoRotateDays <= 0 disables rotation. Callers pass the token
// creation time as the baseline when it has never been rotated. Mirrors
// entity.NeedsDailyResetAt so the decision is testable without the wall clock.
func NeedsRotationAt(autoRotateDays int, rotatedAt, now int64) bool {
	if autoRotateDays <= 0 {
		return false
	}
	return now-rotatedAt >= int64(autoRotateDays)*secondsPerDay
}

// RotateDueTokens rotates every enabled token whose auto-rotation interval has
// elapsed as of now. For each rotated token it: generates a fresh key, stamps
// rotated_at = now, records an auth.token_rotated audit event carrying the old
// key prefix and old expiry (so an operator can remediate within the window —
// the old secret itself is intentionally not retained), and best-effort emails
// the owner. Returns the number of tokens rotated. now and send are injected
// for deterministic, SMTP-free tests.
//
// Intended to run only on the HA leader (see lifecycle.StartSecretRotation),
// so each due token is rotated exactly once across a multi-replica deployment.
func RotateDueTokens(ctx context.Context, now int64, send EmailSender) (int, error) {
	tokens, err := repo.ListAutoRotateTokens()
	if err != nil {
		return 0, fmt.Errorf("list auto-rotate tokens: %w", err)
	}

	rotated := 0
	for _, token := range tokens {
		select {
		case <-ctx.Done():
			return rotated, ctx.Err()
		default:
		}

		baseline := token.RotatedAt
		if baseline == 0 {
			baseline = token.CreatedTime
		}
		if baseline == 0 {
			// Unknown age (neither rotated_at nor created_time set, e.g. a
			// malformed or imported row): skip rather than force-rotate a key
			// of unknown age out from under its owner on the first scan.
			continue
		}
		if !NeedsRotationAt(token.AutoRotateDays, baseline, now) {
			continue
		}

		oldPrefix := keyPrefix(token.Key)
		oldExpired := token.ExpiredTime

		newKey, err := GenerateTokenKey()
		if err != nil {
			common.SysError(fmt.Sprintf("secret rotation: generate key for token %d: %v", token.Id, err))
			continue
		}
		// CAS on the persisted rotated_at (NOT the CreatedTime fallback): if a
		// concurrent rotator — the manual /internal/admin/rotate-due-tokens
		// trigger, or a second replica during a brief leadership split-brain —
		// already rotated this token, the guarded UPDATE matches zero rows and
		// we skip it: no double rotation, no audit event or owner email for a
		// rotation that did not happen here.
		//nolint:contextcheck // the cache refresh inside is intentionally detached fire-and-forget (same as rotateKey)
		if err := token.RotateKeyWithTimestampCAS(newKey, token.RotatedAt, now); err != nil {
			if errors.Is(err, repo.ErrRotationRaceLost) {
				common.SysLog(fmt.Sprintf("secret rotation: token %d already rotated concurrently, skipping", token.Id))
				continue
			}
			common.SysError(fmt.Sprintf("secret rotation: rotate token %d: %v", token.Id, err))
			continue
		}

		details := fmt.Sprintf(`{"reason":"auto_rotate","auto_rotate_days":%d,"old_key_prefix":%q,"old_expired_time":%d}`,
			token.AutoRotateDays, oldPrefix, oldExpired)
		governance.RecordAuditEvent(governance.NewDetachedAuditEvent(
			token.TenantId, governance.ActorSystem, token.UserId,
			governance.ActionAuthTokenRotated, governance.ResourceToken, token.Id, details))

		notifyOwner(token, oldPrefix, send)
		rotated++
	}
	return rotated, nil
}

// notifyOwner best-effort emails the token owner about a rotation. Failures are
// logged but never abort the rotation pass.
func notifyOwner(token *repo.Token, oldPrefix string, send EmailSender) {
	if send == nil {
		return
	}
	email, err := repo.GetUserEmail(token.UserId)
	if err != nil || email == "" {
		return
	}
	subject := "API token rotated"
	content := fmt.Sprintf(
		"Your API token %q was automatically rotated on schedule (every %d days). "+
			"The previous key (prefix %s…) is no longer valid — update your integrations "+
			"with the new key from the console.",
		token.Name, token.AutoRotateDays, oldPrefix)
	if err := send(subject, email, content); err != nil {
		common.SysError(fmt.Sprintf("secret rotation: notify owner of token %d: %v", token.Id, err))
	}
}

func keyPrefix(key string) string {
	if len(key) <= rotatedKeyPrefixLen {
		return key
	}
	return key[:rotatedKeyPrefixLen]
}
