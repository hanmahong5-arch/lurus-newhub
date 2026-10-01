package systemone

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
	"github.com/LurusTech/lurus-hub/internal/pkg/dto"

	"github.com/gin-gonic/gin"
)

// fixture is one captured exchange. testdata/laya holds real captures from a
// running laya-serve; testdata/typesafe holds api.md examples and the real
// no-key/bad-key probes, plus clearly labelled synthetic bodies for statuses
// the hosted API was never observed to return.
type fixture struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Request struct {
		Body json.RawMessage `json:"body"`
	} `json:"request"`
	Response struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    json.RawMessage   `json:"body"`
	} `json:"response"`
}

func loadFixtures(t *testing.T, dir string) []fixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", dir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures under testdata/%s (err %v)", dir, err)
	}
	sort.Strings(paths)
	out := make([]fixture, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var f fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out = append(out, f)
	}
	return out
}

func loadFixture(t *testing.T, dir, name string) fixture {
	t.Helper()
	for _, f := range loadFixtures(t, dir) {
		if f.Name == name || strings.TrimSuffix(filepath.Base(name), ".json") == f.Name {
			return f
		}
	}
	t.Fatalf("fixture %s/%s not found", dir, name)
	return fixture{}
}

// wireBody is what the upstream sent: a JSON string body is a non-JSON page
// (e.g. an edge error) served verbatim, anything else is the JSON document.
func (f fixture) wireBody() []byte {
	var s string
	if json.Unmarshal(f.Response.Body, &s) == nil {
		return []byte(s)
	}
	return f.Response.Body
}

// exchange is what the fake upstream saw of the adaptor's request.
type exchange struct {
	Method, Path, Authorization string
	Body                        []byte
	Resp                        *http.Response
}

// replay sends the adaptor's own request (URL, headers, converted body) to an
// httptest server that answers with the fixture, and returns the live
// *http.Response the relay layer would hand to DoResponse/SystemOneError.
func replay(t *testing.T, f fixture, channelType int, key string) (*exchange, *relaycommon.RelayInfo) {
	t.Helper()
	ex := &exchange{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ex.Method, ex.Path, ex.Authorization = r.Method, r.URL.Path, r.Header.Get("Authorization")
		ex.Body, _ = io.ReadAll(r.Body)
		for k, v := range f.Response.Headers {
			if strings.EqualFold(k, "content-length") {
				continue // the body is re-serialised, so the recorded length is stale
			}
			w.Header().Set(k, v)
		}
		w.WriteHeader(f.Response.Status)
		_, _ = w.Write(f.wireBody())
	}))
	t.Cleanup(srv.Close)

	info := infoFor(channelType, srv.URL+"/", key)
	req := &dto.SystemOneRequest{}
	if json.Unmarshal(f.Request.Body, req) != nil || req.Questions == nil {
		// Some captures are deliberately malformed bodies; the replay still
		// needs a request to send.
		req = &dto.SystemOneRequest{State: json.RawMessage(`"s"`), Model: "m", Questions: map[string]dto.SystemOneQuestion{"q": {Type: "noul"}}}
	}
	info.OriginModelName = req.Model
	info.UpstreamModelName = req.Model

	a := &Adaptor{}
	converted, err := a.ConvertSystemOneRequest(nil, info, req)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	body, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	url, err := a.GetRequestURL(info)
	if err != nil {
		t.Fatal(err)
	}
	hreq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetupRequestHeader(nil, &hreq.Header, info); err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	ex.Resp = resp
	t.Cleanup(func() { _ = resp.Body.Close() })
	return ex, info
}

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	return c, w
}
