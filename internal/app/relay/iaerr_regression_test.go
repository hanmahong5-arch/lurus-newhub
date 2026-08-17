package relay

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"

	"github.com/gin-gonic/gin"
)

// A task row whose `data` column holds bytes that are not valid JSON. Task.Data
// is a json.RawMessage, so marshalling a TaskDto built from this row fails
// inside encoding/json. Any upstream provider writing a truncated / non-JSON
// payload produces exactly this state.
var iaerrCorruptTaskData = json.RawMessage(`{"clips":`)

// Guards the marshal error in sunoFetchByIDRespBodyBuilder. When the error was
// dropped, the builder returned (nil, nil) and RelayTaskFetch substituted the
// canned `{"code":"success","data":null}` body — a corrupt row was reported to
// the caller as HTTP 200 success.
func TestIaerrSunoFetchByID_MarshalErrorIsReported(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	seedTask(t, &repo.Task{
		TaskID: "iaerr-1",
		UserId: 4242,
		Action: "song",
		Status: repo.TaskStatusSuccess,
		Data:   iaerrCorruptTaskData,
	})

	c, _ := newJSONContext(http.MethodGet, "/", nil)
	c.Set("id", 4242)
	c.Params = gin.Params{{Key: "id", Value: "iaerr-1"}}

	body, taskErr := sunoFetchByIDRespBodyBuilder(c)
	if taskErr == nil {
		// Explicit return: t.Fatalf is not marked noreturn, so without it
		// staticcheck reads the lines below as a nil dereference (SA5011).
		t.Fatalf("marshal failure was swallowed: taskErr=nil, body=%q", body)
		return
	}
	if taskErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusInternalServerError)
	}
	if taskErr.Code != "marshal_response_failed" {
		t.Errorf("Code = %q, want marshal_response_failed", taskErr.Code)
	}
}

// Same guard for the batch builder: one corrupt row must not be reported as a
// successful fetch of an empty task list.
func TestIaerrSunoFetch_MarshalErrorIsReported(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	seedTask(t, &repo.Task{
		TaskID: "iaerr-2",
		UserId: 4243,
		Action: "song",
		Status: repo.TaskStatusSuccess,
		Data:   iaerrCorruptTaskData,
	})

	c, _ := newJSONContext(http.MethodPost, "/", []byte(`{"ids":["iaerr-2"],"action":"song"}`))
	c.Set("id", 4243)

	body, taskErr := sunoFetchRespBodyBuilder(c)
	if taskErr == nil {
		// Explicit return: see the note in the by-ID test above (SA5011).
		t.Fatalf("marshal failure was swallowed: taskErr=nil, body=%q", body)
		return
	}
	if taskErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", taskErr.StatusCode, http.StatusInternalServerError)
	}
	if taskErr.Code != "marshal_response_failed" {
		t.Errorf("Code = %q, want marshal_response_failed", taskErr.Code)
	}
}

// Sanity anchor: a well-formed row still round-trips, so the guards above fail
// for the intended reason (marshal error) and not because the fixture is broken.
func TestIaerrSunoFetchByID_ValidDataStillSucceeds(t *testing.T) {
	cleanup := setupRelayDB(t)
	defer cleanup()

	seedTask(t, &repo.Task{
		TaskID: "iaerr-3",
		UserId: 4244,
		Action: "song",
		Status: repo.TaskStatusSuccess,
		Data:   json.RawMessage(`{"clips":[]}`),
	})

	c, _ := newJSONContext(http.MethodGet, "/", nil)
	c.Set("id", 4244)
	c.Params = gin.Params{{Key: "id", Value: "iaerr-3"}}

	body, taskErr := sunoFetchByIDRespBodyBuilder(c)
	if taskErr != nil {
		t.Fatalf("unexpected taskErr: %+v", taskErr)
	}
	if len(body) == 0 {
		t.Fatal("expected a marshalled body")
	}
}
