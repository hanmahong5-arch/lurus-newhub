package common

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListWalletTransactionsHTTP_Success verifies the happy path: 200 OK with
// a {data, total} envelope from platform decodes into WalletTransactionsPage.
func TestListWalletTransactionsHTTP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/internal/v1/accounts/42/wallet/transactions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// platform parsePagination reads `page` (not `p`); a `p` key would be
		// ignored and every request would silently return page 1.
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "10" || r.URL.Query().Has("p") {
			t.Errorf("unexpected pagination query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": 1, "account_id": 42, "type": "topup", "amount": 100.0, "balance_after": 100.0, "description": "first"},
				{"id": 2, "account_id": 42, "type": "debit", "amount": 25.5, "balance_after": 74.5, "description": "spend"},
			},
			"total": 7,
		})
	}))
	defer srv.Close()

	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	defer func() { IdentityServiceURL = prev }()

	page, err := ListWalletTransactionsHTTP(context.Background(), 42, 2, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page == nil {
		t.Fatal("expected page, got nil")
	}
	if page.Total != 7 {
		t.Errorf("total = %d, want 7", page.Total)
	}
	if len(page.Data) != 2 {
		t.Fatalf("data len = %d, want 2", len(page.Data))
	}
	if page.Data[0].ID != 1 || page.Data[0].Type != "topup" {
		t.Errorf("data[0] = %+v", page.Data[0])
	}
	if page.Data[1].Amount != 25.5 {
		t.Errorf("data[1].amount = %f, want 25.5", page.Data[1].Amount)
	}
}

// TestListWalletTransactionsHTTP_NotConfigured returns (nil, nil) when the
// platform URL is unset, so the wallet page can render a "feature disabled"
// state instead of crashing.
func TestListWalletTransactionsHTTP_NotConfigured(t *testing.T) {
	prev := IdentityServiceURL
	IdentityServiceURL = ""
	defer func() { IdentityServiceURL = prev }()

	page, err := ListWalletTransactionsHTTP(context.Background(), 1, 1, 20)
	if err != nil || page != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", page, err)
	}
}

// TestListWalletTransactionsHTTP_ServerError returns (nil, nil) on 5xx so the
// handler degrades gracefully — Switch surfaces "服务暂不可用" without leaking
// platform internals.
func TestListWalletTransactionsHTTP_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	defer func() { IdentityServiceURL = prev }()

	page, err := ListWalletTransactionsHTTP(context.Background(), 1, 1, 20)
	if err != nil || page != nil {
		t.Errorf("expected (nil, nil) on 500, got (%v, %v)", page, err)
	}
}

// TestListWalletTransactionsHTTP_DefaultPagination clamps invalid page sizes
// to 20 (and page 0 → 1), matching the contract documented in the function.
func TestListWalletTransactionsHTTP_DefaultPagination(t *testing.T) {
	var gotP, gotSize string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotP = r.URL.Query().Get("page")
		gotSize = r.URL.Query().Get("page_size")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
	}))
	defer srv.Close()

	prev := IdentityServiceURL
	IdentityServiceURL = srv.URL
	defer func() { IdentityServiceURL = prev }()

	if _, err := ListWalletTransactionsHTTP(context.Background(), 1, 0, 500); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotP != "1" {
		t.Errorf("page 0 should clamp to 1, got %q", gotP)
	}
	if gotSize != "20" {
		t.Errorf("page_size 500 should clamp to 20, got %q", gotSize)
	}
}
