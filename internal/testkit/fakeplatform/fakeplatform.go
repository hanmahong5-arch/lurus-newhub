// Package fakeplatform is a stand-in for the lurus-platform internal API that
// newhub calls when unified billing is on: account lookup by OIDC subject,
// the wallet (pre-authorize → settle/release, direct debit, balance) and the
// usage mirrors.
//
// It exists because the production money path is this one — r6-stage runs
// with BILLING_UNIFIED_ENABLED=true — and its unit is different from the
// local ledger's: quota is priced in US dollars, the wallet in yuan. The
// 2026-09-23 defect (#210, every $1 of usage charged as CNY 1) lived exactly
// at that boundary, where no local-ledger assertion can see it. A suite that
// reads this server's ledger can.
//
// Only the calls newhub actually makes are implemented. Anything else under
// /internal/ is answered 404 and listed at GET /_fake/platform/unhandled, so a
// new platform call shows up as a gap instead of passing silently.
//
// Standard library only, for the same reason as fakeupstream.
package fakeplatform

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Account is one platform account.
type Account struct {
	ID         int64   `json:"id"`
	IDPSubject string  `json:"idp_subject"`
	Email      string  `json:"email"`
	Balance    float64 `json:"balance"`
	Frozen     float64 `json:"frozen"`
}

// Entry is one wallet movement. Amounts are in yuan, as the platform keeps
// them, and at the platform's precision (see money).
type Entry struct {
	Time      time.Time `json:"time"`
	AccountID int64     `json:"account_id"`
	// Kind: preauth, settle, release, debit, credit.
	Kind      string  `json:"kind"`
	Amount    float64 `json:"amount"`
	PreAuthID int64   `json:"preauth_id,omitempty"`
	Reference string  `json:"reference,omitempty"`
}

type hold struct {
	accountID int64
	amount    float64
	state     string // active, settled, released
}

// Server is the fake platform. Use New.
type Server struct {
	key string

	mu        sync.Mutex
	accounts  map[int64]*Account
	holds     map[int64]*hold
	nextHold  int64
	ledger    []Entry
	seenKeys  map[string]bool // Idempotency-Key values already applied
	unhandled []string
	billing   BillingConfig
}

// BillingConfig is what GET /internal/v1/billing-config states. newhub applies
// the stated flags over its own env every 30 seconds, so this — not the env —
// decides which money path runs.
type BillingConfig struct {
	UnifiedBillingEnabled bool `json:"unified_billing_enabled"`
	LocalLedgerAdvisory   bool `json:"local_ledger_advisory"`
}

// ProductionBillingConfig is what the real platform stated on 2026-10-03
// (R6, read back from newhub's own log): unified billing on, local ledger
// still enforcing. The fake states the same unless told otherwise, so the
// suite runs the path production runs.
var ProductionBillingConfig = BillingConfig{UnifiedBillingEnabled: true, LocalLedgerAdvisory: false}

// New builds a server that accepts only the given internal key (Bearer).
// An empty key accepts any.
func New(key string) *Server {
	return &Server{
		key:      key,
		accounts: map[int64]*Account{},
		holds:    map[int64]*hold{},
		nextHold: 1000,
		seenKeys: map[string]bool{},
		billing:  ProductionBillingConfig,
	}
}

// SetBillingConfig changes what billing-config states from now on.
func (s *Server) SetBillingConfig(c BillingConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.billing = c
}

// AddAccount creates or replaces an account.
func (s *Server) AddAccount(a Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := a
	cp.Balance, cp.Frozen = money(cp.Balance), money(cp.Frozen)
	s.accounts[a.ID] = &cp
}

// Ledger returns a copy of every wallet movement.
func (s *Server) Ledger() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.ledger...)
}

// Charged is what the account has actually paid: settled holds plus direct
// debits, minus credits. Holds that are only frozen are not charges.
func (s *Server) Charged(accountID int64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chargedLocked(accountID)
}

func (s *Server) chargedLocked(accountID int64) float64 {
	var sum float64
	for _, e := range s.ledger {
		if e.AccountID != accountID {
			continue
		}
		switch e.Kind {
		case "settle", "debit":
			sum += e.Amount
		case "credit":
			sum -= e.Amount
		}
	}
	return money(sum)
}

// Unhandled lists the /internal/ calls this fake does not implement.
func (s *Server) Unhandled() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.unhandled...)
}

// Handler serves /internal/v1/* (the platform API) and /_fake/platform/*
// (control). Mount it at the root.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := s.requireKey

	mux.HandleFunc("GET /internal/v1/accounts/by-idp-sub/{sub}", auth(s.bySubject))
	mux.HandleFunc("GET /internal/v1/accounts/by-zitadel-sub/{sub}", auth(s.bySubject))
	mux.HandleFunc("GET /internal/v1/accounts/{id}/wallet/balance", auth(s.balance))
	mux.HandleFunc("POST /internal/v1/accounts/{id}/wallet/pre-authorize", auth(s.preAuthorize))
	mux.HandleFunc("POST /internal/v1/wallet/pre-auth/{id}/settle", auth(s.settle))
	mux.HandleFunc("POST /internal/v1/wallet/pre-auth/{id}/release", auth(s.release))
	mux.HandleFunc("POST /internal/v1/accounts/{id}/wallet/debit", auth(s.debit))
	mux.HandleFunc("POST /internal/v1/accounts/{id}/wallet/credit", auth(s.credit))
	mux.HandleFunc("GET /internal/v1/accounts/{id}/entitlements/{product}", auth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{})
	}))
	// Usage mirrors move no money; they are accepted and dropped.
	accepted := auth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})
	mux.HandleFunc("POST /internal/v1/usage/report", accepted)
	mux.HandleFunc("POST /internal/v1/usage/events", accepted)
	mux.HandleFunc("GET /internal/v1/billing-config", auth(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		c := s.billing
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, c)
	}))
	mux.HandleFunc("/internal/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.unhandled = append(s.unhandled, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_implemented_by_fakeplatform"})
	})

	mux.HandleFunc("POST /_fake/platform/accounts", func(w http.ResponseWriter, r *http.Request) {
		var a Account
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil || a.ID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be an Account with id > 0"})
			return
		}
		s.AddAccount(a)
		writeJSON(w, http.StatusOK, a)
	})
	mux.HandleFunc("GET /_fake/platform/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		s.mu.Lock()
		defer s.mu.Unlock()
		a, ok := s.accounts[id]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such account"})
			return
		}
		var entries []Entry
		for _, e := range s.ledger {
			if e.AccountID == id {
				entries = append(entries, e)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"account": a, "charged": s.chargedLocked(id), "ledger": entries})
	})
	mux.HandleFunc("POST /_fake/platform/billing-config", func(w http.ResponseWriter, r *http.Request) {
		var c BillingConfig
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "body must be a BillingConfig"})
			return
		}
		s.SetBillingConfig(c)
		writeJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("GET /_fake/platform/ledger", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ledger": s.Ledger()})
	})
	mux.HandleFunc("GET /_fake/platform/unhandled", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"unhandled": s.Unhandled()})
	})
	return mux
}

func (s *Server) requireKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.key != "" && r.Header.Get("Authorization") != "Bearer "+s.key {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) bySubject(w http.ResponseWriter, r *http.Request) {
	sub := r.PathValue("sub")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.accounts {
		if a.IDPSubject != "" && a.IDPSubject == sub {
			writeJSON(w, http.StatusOK, map[string]any{
				"id": a.ID, "lurus_id": "LU" + strconv.FormatInt(a.ID, 10),
				"idp_subject": a.IDPSubject, "zitadel_sub": a.IDPSubject, "email": a.Email,
			})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "account_not_found"})
}

// account looks up the {id} path value. Callers hold s.mu.
func (s *Server) account(w http.ResponseWriter, r *http.Request) (*Account, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a, ok := s.accounts[id]
	if err != nil || !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account_not_found"})
		return nil, false
	}
	return a, true
}

// replayed reports whether this Idempotency-Key was already applied, marking
// it applied otherwise. Requests without a key are never deduplicated.
// Callers hold s.mu.
func (s *Server) replayed(r *http.Request) bool {
	k := r.Header.Get("Idempotency-Key")
	if k == "" {
		return false
	}
	if s.seenKeys[r.URL.Path+"|"+k] {
		return true
	}
	s.seenKeys[r.URL.Path+"|"+k] = true
	return false
}

func (s *Server) balance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.account(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": a.Balance, "frozen": a.Frozen})
}

func (s *Server) preAuthorize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount      float64 `json:"amount"`
		ReferenceID string  `json:"reference_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_amount"})
		return
	}
	req.Amount = money(req.Amount)
	if r.Header.Get("Idempotency-Key") == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "idempotency_key_required"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.account(w, r)
	if !ok {
		return
	}
	if a.Balance-a.Frozen < req.Amount {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "insufficient_balance"})
		return
	}
	s.nextHold++
	id := s.nextHold
	s.holds[id] = &hold{accountID: a.ID, amount: req.Amount, state: "active"}
	a.Frozen = money(a.Frozen + req.Amount)
	s.ledger = append(s.ledger, Entry{Time: time.Now(), AccountID: a.ID, Kind: "preauth", Amount: req.Amount, PreAuthID: id, Reference: req.ReferenceID})
	writeJSON(w, http.StatusCreated, map[string]any{
		"preauth_id": id, "amount": req.Amount, "status": "active",
		"expires_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
	})
}

func (s *Server) holdFor(w http.ResponseWriter, r *http.Request) (int64, *hold, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	h, ok := s.holds[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "preauth_not_found"})
		return 0, nil, false
	}
	return id, h, true
}

func (s *Server) settle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActualAmount float64 `json:"actual_amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ActualAmount < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_amount"})
		return
	}
	req.ActualAmount = money(req.ActualAmount)
	s.mu.Lock()
	defer s.mu.Unlock()
	id, h, ok := s.holdFor(w, r)
	if !ok {
		return
	}
	resp := map[string]any{"preauth_id": id, "status": "settled", "held_amount": h.amount, "actual_amount": req.ActualAmount}
	if h.state == "settled" && s.replayed(r) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if h.state != "active" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "preauth_not_active"})
		return
	}
	s.replayed(r)
	a := s.accounts[h.accountID]
	// Like the real guard (MS-5): an over-settle is allowed while the balance
	// net of other holds covers it.
	if a.Balance-a.Frozen+h.amount < req.ActualAmount {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "insufficient_balance"})
		return
	}
	a.Frozen = money(a.Frozen - h.amount)
	a.Balance = money(a.Balance - req.ActualAmount)
	h.state = "settled"
	s.ledger = append(s.ledger, Entry{Time: time.Now(), AccountID: a.ID, Kind: "settle", Amount: req.ActualAmount, PreAuthID: id})
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) release(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, h, ok := s.holdFor(w, r)
	if !ok {
		return
	}
	if h.state == "released" {
		writeJSON(w, http.StatusOK, map[string]any{"preauth_id": id, "status": "released"})
		return
	}
	if h.state != "active" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "preauth_not_active"})
		return
	}
	a := s.accounts[h.accountID]
	a.Frozen = money(a.Frozen - h.amount)
	h.state = "released"
	s.ledger = append(s.ledger, Entry{Time: time.Now(), AccountID: a.ID, Kind: "release", Amount: h.amount, PreAuthID: id})
	writeJSON(w, http.StatusOK, map[string]any{"preauth_id": id, "status": "released"})
}

func (s *Server) debit(w http.ResponseWriter, r *http.Request) {
	s.move(w, r, "debit")
}

func (s *Server) credit(w http.ResponseWriter, r *http.Request) {
	s.move(w, r, "credit")
}

func (s *Server) move(w http.ResponseWriter, r *http.Request, kind string) {
	var req struct {
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_amount"})
		return
	}
	req.Amount = money(req.Amount)
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.account(w, r)
	if !ok {
		return
	}
	if s.replayed(r) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "balance_after": a.Balance})
		return
	}
	if kind == "debit" {
		if a.Balance-a.Frozen < req.Amount {
			writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "insufficient_balance"})
			return
		}
		a.Balance = money(a.Balance - req.Amount)
	} else {
		a.Balance = money(a.Balance + req.Amount)
	}
	s.ledger = append(s.ledger, Entry{Time: time.Now(), AccountID: a.ID, Kind: kind, Amount: req.Amount, Reference: strings.TrimSpace(req.Description)})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "balance_after": a.Balance})
}

// money rounds an amount the way the platform's storage does. Its wallet,
// hold and transaction columns are decimal(14,4) (2l-svc-platform
// internal/domain/entity/wallet.go), so PostgreSQL rounds every value newhub
// sends to 0.0001 yuan, half away from zero: a settle of 0.00657 is booked as
// 0.0066. Asserting the unrounded figure would assert a number production
// never stores.
func money(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
