package coze

// Regression cover for the response-body ownership rule inside
// (*Adaptor).DoRequest. Coze's non-stream chat API is asynchronous, so
// DoRequest issues three upstream calls with two different owners:
//
//   - create-chat  -> consumed here, never handed upward  => THIS function closes it
//   - poll/retrieve-> consumed by checkIfChatComplete      => that function closes it
//   - getChatDetail-> returned to the relay layer          => the relay layer closes it
//
// Closing the wrong one truncates the reply the relay layer is about to stream
// back to the client; closing none of them leaks the create-chat response.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/LurusTech/lurus-hub/internal/adapter/provider/common"
)

// bcfixFindMethod returns the declaration of method recv.name in file.
func bcfixFindMethod(file *ast.File, recv, name string) *ast.FuncDecl {
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Name.Name != name {
			continue
		}
		t := fn.Recv.List[0].Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok && id.Name == recv {
			return fn
		}
	}
	return nil
}

// TestBcfixCozeDoRequestClosesCreateChatBody pins that the non-stream branch of
// (*Adaptor).DoRequest closes the create-chat response. That response is fully
// read here and never reaches the relay layer, so nothing downstream will ever
// close it.
//
// The assertion is made on the AST instead of at runtime on purpose: net/http's
// transport already recycles the connection on EOF and tears it down on a read
// error, so a missing Close() has no effect observable through net/http's public
// surface. What it does leak is the read-deadline timer that
// app.WrapNonStreamReadDeadline arms on every non-stream body — stopped only by
// Close, otherwise live for RELAY_NONSTREAM_READ_TIMEOUT (default 300s) while
// pinning the body and its read buffers — and time.Timer exposes no handle a
// test could count.
func TestBcfixCozeDoRequestClosesCreateChatBody(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "adaptor.go", nil, 0)
	if err != nil {
		t.Fatalf("parse adaptor.go: %v", err)
	}
	fn := bcfixFindMethod(file, "Adaptor", "DoRequest")
	if fn == nil || fn.Body == nil {
		t.Fatal("(*Adaptor).DoRequest not found in adaptor.go")
		return
	}

	var closes int
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Close" {
			return true
		}
		if body, ok := sel.X.(*ast.SelectorExpr); ok && body.Sel.Name == "Body" {
			closes++
		}
		return true
	})

	if closes == 0 {
		t.Fatal("(*Adaptor).DoRequest never closes a response body: the create-chat response is neither returned to the relay layer nor closed here, so it (and the read-deadline timer armed on it) leaks on every non-stream Coze request")
	}
	if closes > 1 {
		t.Fatalf("(*Adaptor).DoRequest closes %d response bodies, want exactly 1 (only the create-chat response); the stream-branch and getChatDetail responses belong to the relay layer", closes)
	}
}

// TestBcfixCozeDoRequestKeepsReturnedDetailBodyOpen is the counterweight to the
// test above: the response DoRequest hands back must still be readable, i.e. the
// create-chat close must not have been applied to the wrong response.
func TestBcfixCozeDoRequestKeepsReturnedDetailBodyOpen(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	const detailBody = `{"code":0,"msg":"","data":[{"id":"m1","type":"answer","content":"pong"}]}`
	var createCalls, retrieveCalls, detailCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/chat":
			createCalls++
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"id":"chat-1","conversation_id":"conv-1","status":"in_progress"}}`))
		case "/v3/chat/retrieve":
			retrieveCalls++
			_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"status":"completed","usage":{"token_count":9,"input_count":4,"output_count":5}}}`))
		case "/v3/chat/message/list":
			detailCalls++
			_, _ = w.Write([]byte(detailBody))
		default:
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}

	result, err := a.DoRequest(c, info, strings.NewReader(`{"messages":[{"role":"user","content":"ping"}]}`))
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	resp, ok := result.(*http.Response)
	if !ok {
		t.Fatalf("result type = %T, want *http.Response (getChatDetail's response)", result)
	}
	defer func() { _ = resp.Body.Close() }()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read returned body: %v -- DoRequest must hand the relay layer an OPEN body", err)
	}
	if string(got) != detailBody {
		t.Errorf("returned body = %q, want the getChatDetail payload %q", got, detailBody)
	}
	if createCalls != 1 || retrieveCalls != 1 || detailCalls != 1 {
		t.Errorf("upstream calls create/retrieve/detail = %d/%d/%d, want 1/1/1", createCalls, retrieveCalls, detailCalls)
	}
	if c.GetString("coze_conversation_id") != "conv-1" || c.GetString("coze_chat_id") != "chat-1" {
		t.Errorf("context ids = %q/%q, want conv-1/chat-1 (parsed out of the create-chat body before it is closed)",
			c.GetString("coze_conversation_id"), c.GetString("coze_chat_id"))
	}
}

// TestBcfixCozeDoRequestCreateChatErrorStillSurfaces guards the early-return
// path: closing the create-chat body must not swallow or alter the upstream
// error code that aborts the orchestration.
func TestBcfixCozeDoRequestCreateChatErrorStillSurfaces(t *testing.T) {
	prov_aws_coze_dify_coze_allowLocalFetch(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/chat" {
			t.Errorf("unexpected upstream path %s: a failed create-chat must not be followed by polling", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"code":4000,"msg":"bcfix upstream refused","data":{}}`))
	}))
	defer upstream.Close()

	a := &Adaptor{}
	c, _ := prov_aws_coze_dify_coze_newTestContext()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: upstream.URL, ApiKey: "sk-test"}}

	result, err := a.DoRequest(c, info, strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected an error for a non-zero create-chat code")
	}
	if !strings.Contains(err.Error(), "bcfix upstream refused") {
		t.Errorf("error = %q, want the upstream msg preserved", err.Error())
	}
	if result != nil {
		t.Errorf("result = %v, want nil on the create-chat error path", result)
	}
}
