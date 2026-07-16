package handler

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/LurusTech/lurus-hub/internal/pkg/tavily"

	"github.com/gin-gonic/gin"
)

// webSearchClient is lazy-initialized on first use so unrelated tests
// don't need TAVILY_API_KEY in the env. Resolved exactly once per
// process lifetime; subsequent calls return the cached instance.
var (
	webSearchClient     *tavily.Client
	webSearchClientErr  error
	webSearchClientOnce sync.Once
)

func resolveWebSearchClient() (*tavily.Client, error) {
	webSearchClientOnce.Do(func() {
		key := os.Getenv("TAVILY_API_KEY")
		if key == "" {
			webSearchClientErr = ErrSearchDisabled
			return
		}
		webSearchClient, webSearchClientErr = tavily.New(key)
	})
	return webSearchClient, webSearchClientErr
}

// ErrSearchDisabled is the sentinel returned by resolveWebSearchClient
// when TAVILY_API_KEY isn't set. Handlers translate this into a 503
// with a structured payload so the client can fall back gracefully.
var ErrSearchDisabled = &searchDisabledError{}

type searchDisabledError struct{}

func (*searchDisabledError) Error() string {
	return "web_search is not configured (TAVILY_API_KEY missing)"
}

// WebSearchRequest is the inbound shape from Lutu APP (and any other
// caller). Deliberately tiny — keeps the wire surface stable across
// Tavily SDK upgrades.
type WebSearchRequest struct {
	Query string `json:"query" binding:"required"`
	// Depth: "basic" (default) or "advanced". Advanced costs ~2x latency
	// but pulls in more sources; leave empty for basic.
	Depth string `json:"depth,omitempty"`
	// MaxResults: 1..10. 0 → server default (5).
	MaxResults int `json:"max_results,omitempty"`
}

// WebSearchResult is one entry returned to the caller. Mirrors the
// internal tavily.SearchResult but lives in this package so the handler
// is the single source of truth for Lutu APP's contract.
type WebSearchResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// WebSearchResponse is what the client sees. Answer may be empty when
// Tavily declines to summarize; clients should fall through to the
// results list in that case.
type WebSearchResponse struct {
	Answer  string            `json:"answer"`
	Results []WebSearchResult `json:"results"`
}

// PostWebSearch handles `POST /api/v2/lutu/search`. It is a thin
// pass-through to Tavily so the API key stays server-side — the Lutu
// APP never sees the Tavily credential.
//
// Auth: enforced by middleware.RequireOIDCToken in the router — a
// valid OIDC JWT (the bearer the Lutu APP already attaches) is
// required, so the shared Tavily quota can't be drained anonymously.
// The verified subject is available as `oidc_user_id` in the gin
// context for optional per-user accounting.
func PostWebSearch(c *gin.Context) {
	var req WebSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "bad_request",
			"message": "query is required",
			"detail":  err.Error(),
		})
		return
	}

	client, err := resolveWebSearchClient()
	if err == ErrSearchDisabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "search_disabled",
			"message": "联网搜索暂未配置",
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "search_client_init_failed",
			"message": err.Error(),
		})
		return
	}

	depth := tavily.DepthBasic
	if req.Depth == "advanced" {
		depth = tavily.DepthAdvanced
	}

	// Cap the per-request timeout independently of the Tavily client
	// default so a slow upstream can't pin a Gin worker. 8s is the worst-
	// case "advanced + 10 results" + summarization budget.
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	resp, err := client.Search(ctx, tavily.SearchRequest{
		Query:         req.Query,
		SearchDepth:   depth,
		MaxResults:    req.MaxResults,
		IncludeAnswer: true,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":   "upstream_error",
			"message": "联网搜索失败",
			"detail":  err.Error(),
		})
		return
	}

	out := WebSearchResponse{
		Answer:  resp.Answer,
		Results: make([]WebSearchResult, len(resp.Results)),
	}
	for i, r := range resp.Results {
		out.Results[i] = WebSearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
			Score:   r.Score,
		}
	}
	c.JSON(http.StatusOK, out)
}

// resetWebSearchClientForTesting wipes the lazy-init cache so tests can
// re-resolve with a different env. Exposed only within this package.
func resetWebSearchClientForTesting() {
	webSearchClient = nil
	webSearchClientErr = nil
	webSearchClientOnce = sync.Once{}
}
