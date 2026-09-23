package repo

import (
	"testing"
	"time"

	"github.com/LurusTech/lurus-hub/internal/domain/entity"
)

// Leaderboard dimensions key/user/product (cycle 17): "who and what is
// spending" — per API key (owner + key name), per member, per calling
// product. The same body runs on the hermetic SQLite tier and on real
// PostgreSQL (SetupTestDB, skipped without TEST_POSTGRES_DSN) because the
// key label is built with `||` and the product with a jsonb extract, both
// of which are dialect-sensitive.

func seedRankingDimLog(t *testing.T, tenantID, user, key, other string, prompt, completion int, createdAt int64) {
	t.Helper()
	l := &entity.Log{
		UserId:           1,
		TenantId:         tenantID,
		Type:             LogTypeConsume,
		ModelName:        "m",
		Username:         user,
		TokenName:        key,
		Other:            other,
		Quota:            1,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		CreatedAt:        createdAt,
	}
	if err := LOG_DB.Create(l).Error; err != nil {
		t.Fatalf("seed ranking dimension log: %v", err)
	}
}

func assertRankingDimensions(t *testing.T) {
	t.Helper()
	now := time.Now().Unix()
	start, end := now-3600, now
	// alice uses two keys; bob's key is also called "default"; one row has
	// neither a user nor a key; another tenant's traffic must not count.
	seedRankingDimLog(t, "t1", "alice", "default", `{"source_product":"kova"}`, 100, 0, now-60)
	seedRankingDimLog(t, "t1", "alice", "ci", `{"source_product":"kova"}`, 10, 0, now-60)
	seedRankingDimLog(t, "t1", "bob", "default", `{"cache_tokens":3}`, 1000, 0, now-60)
	seedRankingDimLog(t, "t1", "", "", "", 1, 0, now-60)
	seedRankingDimLog(t, "t2", "alice", "default", `{"source_product":"lutu"}`, 99999, 0, now-60)

	tokensByName := func(by string) map[string]int64 {
		t.Helper()
		rows, _, _, err := GetRankings(start, end, "t1", by, 20)
		if err != nil {
			t.Fatalf("GetRankings by=%s: %v", by, err)
		}
		out := map[string]int64{}
		for _, r := range rows {
			out[r.Name] = r.TotalTokens
		}
		return out
	}

	key := tokensByName("key")
	wantKey := map[string]int64{
		"alice / default":      100,
		"alice / ci":           10,
		"bob / default":        1000,
		"(unknown) / (no key)": 1,
	}
	if len(key) != len(wantKey) {
		t.Errorf("by=key rows = %v, want %v", key, wantKey)
	}
	for name, want := range wantKey {
		if key[name] != want {
			t.Errorf("by=key %q = %d, want %d (all: %v)", name, key[name], want, key)
		}
	}

	user := tokensByName("user")
	if user["alice"] != 110 || user["bob"] != 1000 || user["(unknown)"] != 1 || len(user) != 3 {
		t.Errorf("by=user = %v, want alice 110, bob 1000, (unknown) 1", user)
	}

	product := tokensByName("product")
	// Untagged rows (bob's JSON without the key, and the empty Other) fold
	// into the default product, as GetSpendByProduct does.
	if product["kova"] != 110 || product["llm-api"] != 1001 || len(product) != 2 {
		t.Errorf("by=product = %v, want kova 110, llm-api 1001 (lutu is another tenant)", product)
	}

	pts, err := GetRankingSeries(start, end, "t1", "key", []string{"bob / default"})
	if err != nil {
		t.Fatalf("GetRankingSeries by=key: %v", err)
	}
	var seriesTokens int64
	for _, p := range pts {
		if p.Name != "bob / default" {
			t.Fatalf("series carries %q, which was not asked for: %+v", p.Name, pts)
		}
		seriesTokens += p.Tokens
	}
	if seriesTokens != 1000 {
		t.Errorf("by=key series tokens = %d, want 1000", seriesTokens)
	}

	pts, err = GetRankingSeries(start, end, "t1", "product", []string{"llm-api"})
	if err != nil {
		t.Fatalf("GetRankingSeries by=product: %v", err)
	}
	seriesTokens = 0
	for _, p := range pts {
		seriesTokens += p.Tokens
	}
	if seriesTokens != 1001 {
		t.Errorf("by=product series tokens for llm-api = %d, want 1001", seriesTokens)
	}
}

func TestGetRankings_KeyUserProductDimensions(t *testing.T) {
	cleanup := setupSQLiteDB(t)
	defer cleanup()
	assertRankingDimensions(t)
}

func TestGetRankings_KeyUserProductDimensions_PG(t *testing.T) {
	cleanup := SetupTestDB(t)
	defer cleanup()
	assertRankingDimensions(t)
}
