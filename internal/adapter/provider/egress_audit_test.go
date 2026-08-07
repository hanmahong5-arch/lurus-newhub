package provider

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/app/governance"
	"github.com/LurusTech/lurus-hub/internal/domain/entity"
	"github.com/LurusTech/lurus-hub/internal/pkg/privateendpoint"

	"github.com/gin-gonic/gin"
)

// captureWriter collects audit events written through governance.RecordAuditEvent.
type captureWriter struct {
	events chan *entity.AuditEvent
}

func (w *captureWriter) CreateAuditEvent(e *entity.AuditEvent) error {
	select {
	case w.events <- e:
	default:
	}
	return nil
}

func newCaptureWriter(t *testing.T) *captureWriter {
	t.Helper()
	w := &captureWriter{events: make(chan *entity.AuditEvent, 4)}
	governance.SetAuditWriter(w)
	return w
}

func blockedTestContext(t *testing.T, tenant string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("tenant_id", tenant)
	return c
}

// A refused public target must leave a durable, tenant-attributed record.
// "No prompt left the network" is only auditable if the stopped attempts are
// on the record; an empty log otherwise looks identical to a guard that never
// ran.
func TestRecordDispatchBlocked_WritesTenantAttributedAuditEvent(t *testing.T) {
	w := newCaptureWriter(t)
	c := blockedTestContext(t, "privacy-strict")

	info := &relaycommon.RelayInfo{
		UserId:          9200,
		TokenId:         7,
		OriginModelName: "onprem-chat-8b",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:   42,
			ChannelType: 57,
		},
	}
	// Exactly what ValidateBaseURL produces for a public host.
	err := privateendpoint.ValidateBaseURL("https://api.example-saas.com/v1")
	var blocked *privateendpoint.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("a public host must yield *BlockedError, got %T: %v", err, err)
	}

	recordDispatchBlocked(c, info, err)

	var ev *entity.AuditEvent
	select {
	case ev = <-w.events:
	case <-time.After(3 * time.Second):
		t.Fatal("no audit event was recorded for a blocked dispatch")
	}

	if ev.Action != governance.ActionEgressBlocked {
		t.Errorf("action = %q, want %q", ev.Action, governance.ActionEgressBlocked)
	}
	// Tenant attribution is the point: an event filed under "default" would be
	// invisible to the tenant admin who needs to see it.
	if ev.TenantID != "privacy-strict" {
		t.Errorf("tenant = %q, want privacy-strict", ev.TenantID)
	}
	if ev.Resource != governance.ResourceChannel || ev.ResourceID != 42 {
		t.Errorf("resource = %s/%d, want channel/42", ev.Resource, ev.ResourceID)
	}
	if ev.ActorID != 9200 {
		t.Errorf("actor id = %d, want 9200", ev.ActorID)
	}

	var details map[string]any
	if err := json.Unmarshal([]byte(ev.Details), &details); err != nil {
		t.Fatalf("details is not JSON: %v (%s)", err, ev.Details)
	}
	if details["attempted_host"] != "api.example-saas.com" {
		t.Errorf("attempted_host = %v, want api.example-saas.com", details["attempted_host"])
	}
	if details["request_sent"] != false {
		t.Errorf("request_sent = %v, want false — the whole claim is that nothing was emitted", details["request_sent"])
	}
	if reason, _ := details["reason"].(string); reason == "" {
		t.Error("details must carry the classifier's reason, not just a boolean")
	}
}

// The full base URL may embed credentials (http://user:pass@host). Only the
// host is security-relevant, so only the host is recorded.
func TestRecordDispatchBlocked_DoesNotRecordCredentialsFromBaseURL(t *testing.T) {
	w := newCaptureWriter(t)
	c := blockedTestContext(t, "privacy-strict")

	err := privateendpoint.ValidateBaseURL("https://user:hunter2@api.example-saas.com/v1")
	if err == nil {
		t.Fatal("a public host must be refused regardless of embedded credentials")
	}
	recordDispatchBlocked(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}, err)

	select {
	case ev := <-w.events:
		if strings.Contains(ev.Details, "hunter2") {
			t.Fatalf("audit details leaked embedded credentials: %s", ev.Details)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no audit event was recorded")
	}
}

// A malformed base URL is a configuration typo, not an attempted egress.
// Filing it under a security action would dilute the trail into uselessness.
func TestRecordDispatchBlocked_IgnoresNonEgressErrors(t *testing.T) {
	w := newCaptureWriter(t)
	c := blockedTestContext(t, "privacy-strict")

	for name, err := range map[string]error{
		"plain error":   errors.New("upstream unreachable"),
		"malformed URL": privateendpoint.ValidateBaseURL("ftp://not-http"),
		"empty URL":     privateendpoint.ValidateBaseURL(""),
	} {
		if err == nil {
			t.Fatalf("%s: expected a non-nil error to test with", name)
		}
		recordDispatchBlocked(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}}, err)
		// recordDispatchBlocked returns before dispatching a goroutine when the
		// error does not qualify, so an empty channel here is deterministic.
		if len(w.events) != 0 {
			t.Fatalf("%s: must not produce a security.egress_blocked event", name)
		}
	}
}

// ChannelMeta is an embedded pointer that is nil until InitChannelMeta runs.
// This function sits on the relay path for every adaptor, so a nil there must
// degrade to a partial audit row — never to a panic that takes down a request
// which the guard had already handled correctly.
func TestRecordDispatchBlocked_SurvivesNilChannelMeta(t *testing.T) {
	w := newCaptureWriter(t)
	c := blockedTestContext(t, "privacy-strict")

	err := privateendpoint.ValidateBaseURL("https://api.example-saas.com/v1")
	// &RelayInfo{} leaves ChannelMeta nil, exactly as it is before init.
	recordDispatchBlocked(c, &relaycommon.RelayInfo{}, err)

	select {
	case ev := <-w.events:
		if ev.Action != governance.ActionEgressBlocked {
			t.Errorf("action = %q, want %q", ev.Action, governance.ActionEgressBlocked)
		}
		if ev.ResourceID != 0 {
			t.Errorf("resource id = %d, want 0 when channel meta is absent", ev.ResourceID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no audit event recorded — a nil ChannelMeta must degrade, not drop the event")
	}
}

// An intranet target must pass, and pass silently — no audit noise on the
// happy path.
func TestValidateBaseURL_IntranetProducesNoBlockedError(t *testing.T) {
	if err := privateendpoint.ValidateBaseURL("http://127.0.0.1:11400"); err != nil {
		t.Fatalf("loopback must be accepted: %v", err)
	}
}
