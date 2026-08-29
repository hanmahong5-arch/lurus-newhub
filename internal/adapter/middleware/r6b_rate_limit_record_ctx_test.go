// Copyright (c) 2026 LurusTech
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package middleware

// r6b_rate_limit_record_ctx_test.go pins the lifetime of the post-response
// success recording in redisRateLimitHandler. The recording used to run on
// c.Request.Context(), which the server cancels the moment the client hangs
// up — and on the live gateway the client has ALWAYS hung up by then: the
// relay pipeline settles quota (hundreds of ms of DB writes) between writing
// the response and returning through the middleware, and both one-shot
// clients and the host nginx (no upstream keepalive) close the connection as
// soon as they have the full response. Result, proven live 2026-08-29: three
// one-shot relay probes each answered 200 and recorded nothing; two requests
// reusing a single connection recorded the first and lost the second. The
// success dimension is the only one the shipped defaults arm
// (setting/rate_limit.go:52), so the ceiling could never trip at all.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestRedisRateLimitHandler_RecordsAfterClientDisconnect drives the exact
// production sequence: the check side runs while the request context is
// alive, the handler writes 200 and the client disconnects (context
// canceled), then the middleware unwinds into the recording. The recording
// must land anyway — it documents something that already happened and must
// not share the request's lifetime.
func TestRedisRateLimitHandler_RecordsAfterClientDisconnect(t *testing.T) {
	_, rdb, cleanup := withMiniRedis(t)
	defer cleanup()

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()

	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("id", 990501); c.Next() })
	r.GET("/m", redisRateLimitHandler(60, 0, 1000), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		// The client hangs up the moment it has the response — before the
		// middleware stack unwinds. net/http propagates that as context
		// cancellation.
		cancelReq()
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/m", nil).WithContext(reqCtx))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the disconnect happens after the response, it must not affect the outcome", w.Code)
	}

	n, err := rdb.LLen(context.Background(), "rateLimit:MRRLS:990501").Result()
	if err != nil {
		t.Fatalf("LLen: %v", err)
	}
	if n != 1 {
		t.Errorf("success recording length = %d, want 1 — a client disconnect after the response must not lose the recording (the armed ceiling never trips if successes are never counted)", n)
	}
}
