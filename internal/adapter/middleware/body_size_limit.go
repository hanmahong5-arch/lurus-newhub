package middleware

import (
	"net/http"

	"github.com/LurusTech/lurus-hub/internal/pkg/constant"

	"github.com/gin-gonic/gin"
)

// RequestBodySizeLimit caps the request body at constant.MaxRequestBodyMB
// (the same source of truth DecompressRequestMiddleware uses) without doing
// any decompression/decoding. It exists because DecompressRequestMiddleware
// is only wired onto the relay router (SetRelayRouter), which main.go's
// SetRouter calls AFTER SetApiRouter/SetApiV2Router have already created
// their router.Group("/api") / router.Group("/api/v2") groups — gin snapshots
// a group's middleware chain at Group() call time, so a later engine-level
// Use() never reaches those already-built groups. /api and /api/v2 were left
// with no body-size cap at all, so a client could send an arbitrarily large
// body at any ShouldBindJSON endpoint under those groups and force the
// process to buffer/allocate the whole thing (OOM DoS on the shared pod).
//
// Mount this directly on the /api and /api/v2 RouterGroups (in the same
// function that creates them, before route registration) rather than relying
// on an engine-level Use().
func RequestBodySizeLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			maxMB := constant.MaxRequestBodyMB
			if maxMB <= 0 {
				maxMB = 32
			}
			maxBytes := int64(maxMB) << 20
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
