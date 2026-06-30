package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// IdentityServiceURL is the base URL for the lurus-platform core service.
var IdentityServiceURL = getIdentityServiceURL()

func getIdentityServiceURL() string {
	if url := os.Getenv("IDENTITY_SERVICE_URL"); url != "" {
		return url
	}
	return "http://platform-core.lurus-platform.svc.cluster.local:18104"
}

// IdentityServiceInternalKey is the bearer token for /internal/v1/* endpoints.
var IdentityServiceInternalKey = os.Getenv("IDENTITY_SERVICE_INTERNAL_KEY")

// IdentityAuthRedirect controls whether register/login/topup endpoints redirect to identity service.
// Set IDENTITY_AUTH_REDIRECT=true to enable.
var IdentityAuthRedirect = os.Getenv("IDENTITY_AUTH_REDIRECT") == "true"

// IdentityPublicURL is the external-facing URL for lurus-platform (used in redirect responses).
var IdentityPublicURL = getIdentityPublicURL()

func getIdentityPublicURL() string {
	if url := os.Getenv("IDENTITY_PUBLIC_URL"); url != "" {
		return url
	}
	return "https://identity.lurus.cn"
}

var identityClient = &http.Client{
	Timeout: 5 * time.Second,
}

// IdentityMapping represents the unified user identity mapping returned by lurus-platform.
type IdentityMapping struct {
	ID      int64  `json:"id"`
	LurusID string `json:"lurus_id"`
	// ZitadelSub decodes the OIDC subject from platform responses. platform now
	// emits idp_subject (canonical) alongside the deprecated zitadel_sub; this
	// field accepts either so the Go name can stay stable while the wire migrates.
	// TODO(idp-migration): rename the Go field to IDPSubject once all platform
	// responses have dropped the legacy zitadel_sub key.
	ZitadelSub  string    `json:"idp_subject"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Status      int16     `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// UnmarshalJSON decodes IdentityMapping accepting BOTH the canonical
// idp_subject and the deprecated zitadel_sub subject keys (platform double-writes
// during the migration window). idp_subject wins when both are present.
func (m *IdentityMapping) UnmarshalJSON(data []byte) error {
	type alias IdentityMapping
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*m = IdentityMapping(a)
	if m.ZitadelSub == "" {
		var legacy struct {
			ZitadelSub string `json:"zitadel_sub"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil && legacy.ZitadelSub != "" {
			m.ZitadelSub = legacy.ZitadelSub
		}
	}
	return nil
}

// Entitlements is a key→value map describing an account's product permissions.
type Entitlements map[string]string

// GetString returns a string entitlement value, falling back to defaultVal.
func (e Entitlements) GetString(key, defaultVal string) string {
	if v, ok := e[key]; ok {
		return v
	}
	return defaultVal
}

// GetInt returns an integer entitlement value, falling back to defaultVal.
func (e Entitlements) GetInt(key string, defaultVal int) int {
	v := e.GetString(key, "")
	if v == "" {
		return defaultVal
	}
	var i int
	if _, err := fmt.Sscanf(v, "%d", &i); err != nil {
		return defaultVal
	}
	return i
}

// GetBool returns a boolean entitlement value, falling back to defaultVal.
func (e Entitlements) GetBool(key string, defaultVal bool) bool {
	v := e.GetString(key, "")
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return defaultVal
	}
}

// GetAccountByZitadelSub retrieves account info from lurus-platform by OIDC sub.
// It calls platform's canonical /by-idp-sub/ route (platform serves both
// /by-idp-sub/ and the deprecated /by-zitadel-sub/ on the same handler).
// The Go name is kept stable while the underlying route/wire migrates.
// Returns nil on not-found or network errors (callers degrade gracefully).
func GetAccountByZitadelSub(ctx context.Context, sub string) (*IdentityMapping, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		IdentityServiceURL+"/internal/v1/accounts/by-idp-sub/"+sub,
		nil,
	)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetAccountByZitadelSub: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity GetAccountByZitadelSub: status %d", resp.StatusCode))
		return nil, nil
	}
	var a IdentityMapping
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil, nil
	}
	return &a, nil
}

// UpsertAccount creates or updates an account in lurus-platform (called on OIDC login).
// The body uses the canonical idp_subject field (platform accepts idp_subject or
// the deprecated zitadel_sub — see platform UpsertAccountRequest). The Go param
// name is kept for caller stability.
func UpsertAccount(ctx context.Context, zitadelSub, email, displayName, avatarURL string) (*IdentityMapping, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]string{
		"idp_subject":  zitadelSub,
		"email":        email,
		"display_name": displayName,
		"avatar_url":   avatarURL,
	})
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		IdentityServiceURL+"/internal/v1/accounts/upsert",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity UpsertAccount: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity UpsertAccount: status %d", resp.StatusCode))
		return nil, nil
	}
	var a IdentityMapping
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil, nil
	}
	return &a, nil
}

// GetEntitlements retrieves product entitlements for an account (Redis-cached in identity service).
// Falls back to empty Entitlements map on any error — callers must handle the free/default case.
func GetEntitlements(ctx context.Context, accountID int64, productID string) (Entitlements, error) {
	if IdentityServiceURL == "" {
		return Entitlements{"plan_code": "free"}, nil
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/entitlements/%s", IdentityServiceURL, accountID, productID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Entitlements{"plan_code": "free"}, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetEntitlements: %v", err))
		return Entitlements{"plan_code": "free"}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Entitlements{"plan_code": "free"}, nil
	}
	var em Entitlements
	if err := json.NewDecoder(resp.Body).Decode(&em); err != nil {
		return Entitlements{"plan_code": "free"}, nil
	}
	return em, nil
}

// AccountOverview mirrors the aggregated read model from lurus-platform's overview endpoint.
type AccountOverview struct {
	Account struct {
		ID          int64  `json:"id"`
		LurusID     string `json:"lurus_id"`
		DisplayName string `json:"display_name"`
		AvatarURL   string `json:"avatar_url"`
	} `json:"account"`
	VIP struct {
		Level          int16  `json:"level"`
		LevelName      string `json:"level_name"`
		LevelEN        string `json:"level_en"`
		Points         int64  `json:"points"`
		LevelExpiresAt *struct {
			Time string `json:"time"`
		} `json:"level_expires_at"`
	} `json:"vip"`
	Wallet struct {
		Balance float64 `json:"balance"`
		Frozen  float64 `json:"frozen"`
	} `json:"wallet"`
	Subscription *struct {
		ProductID string  `json:"product_id"`
		PlanCode  string  `json:"plan_code"`
		Status    string  `json:"status"`
		ExpiresAt *string `json:"expires_at"`
		AutoRenew bool    `json:"auto_renew"`
	} `json:"subscription"`
	TopupURL string `json:"topup_url"`
}

// GetAccountOverview retrieves the aggregated overview for an account from lurus-platform.
// Returns nil, nil on network errors or when identity service is not configured — callers degrade gracefully.
func GetAccountOverview(ctx context.Context, accountID int64, productID string) (*AccountOverview, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/overview", IdentityServiceURL, accountID)
	if productID != "" {
		url += "?product_id=" + productID
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetAccountOverview: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity GetAccountOverview: status %d", resp.StatusCode))
		return nil, nil
	}
	var ov AccountOverview
	if err := json.NewDecoder(resp.Body).Decode(&ov); err != nil {
		return nil, nil
	}
	return &ov, nil
}

// WalletBalance holds the wallet balance information from lurus-platform.
type WalletBalance struct {
	Balance float64 `json:"balance"`
	Frozen  float64 `json:"frozen"`
}

// GetWalletBalance retrieves the wallet balance for an account from lurus-platform.
// Returns nil on errors — callers degrade gracefully.
func GetWalletBalance(ctx context.Context, accountID int64) (*WalletBalance, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/wallet/balance", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetWalletBalance: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity GetWalletBalance: status %d", resp.StatusCode))
		return nil, nil
	}
	var wb WalletBalance
	if err := json.NewDecoder(resp.Body).Decode(&wb); err != nil {
		return nil, nil
	}
	return &wb, nil
}

// GetAccountByEmail retrieves account info from lurus-platform by email address.
// Returns nil on not-found or network errors.
func GetAccountByEmail(ctx context.Context, email string) (*IdentityMapping, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		IdentityServiceURL+"/internal/v1/accounts/by-email/"+email,
		nil,
	)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetAccountByEmail: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var a IdentityMapping
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil, nil
	}
	return &a, nil
}

// GetAccountByZitadelSub_ByAccountID retrieves account info from lurus-platform by account ID.
// Used to resolve identity session tokens to zitadel_sub for user mapping lookup.
func GetAccountByZitadelSub_ByAccountID(ctx context.Context, accountID int64) (*IdentityMapping, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/overview", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetAccountByAccountID: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	// The overview endpoint returns a nested structure; extract the account part.
	// Accept both idp_subject (canonical) and zitadel_sub (deprecated) for the
	// subject during the platform migration window.
	var ov struct {
		Account struct {
			ID          int64  `json:"id"`
			LurusID     string `json:"lurus_id"`
			IDPSubject  string `json:"idp_subject"`
			ZitadelSub  string `json:"zitadel_sub"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			AvatarURL   string `json:"avatar_url"`
			Status      int16  `json:"status"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ov); err != nil {
		return nil, nil
	}
	subject := ov.Account.IDPSubject
	if subject == "" {
		subject = ov.Account.ZitadelSub
	}
	return &IdentityMapping{
		ID:          ov.Account.ID,
		LurusID:     ov.Account.LurusID,
		ZitadelSub:  subject,
		Email:       ov.Account.Email,
		DisplayName: ov.Account.DisplayName,
		AvatarURL:   ov.Account.AvatarURL,
		Status:      ov.Account.Status,
	}, nil
}

// DebitWalletResult holds the response from a wallet debit call.
type DebitWalletResult struct {
	Success      bool    `json:"success"`
	BalanceAfter float64 `json:"balance_after"`
}

// DebitWallet deducts credits from an account's wallet in lurus-platform.
// Returns the remaining balance after the debit, or an error if insufficient balance.
func DebitWallet(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) (*DebitWalletResult, error) {
	if IdentityServiceURL == "" {
		return nil, fmt.Errorf("identity service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"amount":      amount,
		"type":        txType,
		"description": description,
		"product_id":  productID,
	})
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/wallet/debit", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	// Platform money-mutation endpoints require an Idempotency-Key (else 400). A
	// stable per-operation key also dedupes retries against double-charge.
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := identityClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity DebitWallet: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil && errResp.Error == "insufficient_balance" {
			return nil, fmt.Errorf("insufficient_balance")
		}
		return nil, fmt.Errorf("debit failed: status %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debit failed: status %d", resp.StatusCode)
	}
	var result DebitWalletResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// CreditWallet adds credits to an account's wallet in lurus-platform.
// Used for refunds or corrections.
func CreditWallet(ctx context.Context, accountID int64, amount float64, txType, description, productID, idempotencyKey string) error {
	if IdentityServiceURL == "" {
		return fmt.Errorf("identity service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"amount":      amount,
		"type":        txType,
		"description": description,
		"product_id":  productID,
	})
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/wallet/credit", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := identityClient.Do(req)
	if err != nil {
		return fmt.Errorf("identity CreditWallet: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("credit failed: status %d", resp.StatusCode)
	}
	return nil
}

// CheckoutResult holds the response from a cross-service checkout creation.
type CheckoutResult struct {
	OrderNo   string  `json:"order_no"`
	PayURL    string  `json:"pay_url"`
	Status    string  `json:"status"`
	ExpiresAt *string `json:"expires_at"`
}

// CreateCheckout creates a checkout session on lurus-platform for wallet topup.
// The sourceService identifies which product initiated the checkout (e.g., "lurus-api").
func CreateCheckout(ctx context.Context, accountID int64, amountCNY float64, paymentMethod, sourceService, idempotencyKey, returnURL string) (*CheckoutResult, error) {
	if IdentityServiceURL == "" {
		return nil, fmt.Errorf("identity service not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"account_id":      accountID,
		"amount_cny":      amountCNY,
		"payment_method":  paymentMethod,
		"source_service":  sourceService,
		"idempotency_key": idempotencyKey,
		"return_url":      returnURL,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		IdentityServiceURL+"/internal/v1/checkout/create",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity CreateCheckout: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return nil, fmt.Errorf("checkout failed: %s (status %d)", errResp.Error, resp.StatusCode)
	}
	var result CheckoutResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// CheckoutStatus holds the polling response for a checkout order.
type CheckoutStatus struct {
	OrderNo   string  `json:"order_no"`
	Status    string  `json:"status"`
	AmountCNY float64 `json:"amount_cny"`
	PayURL    string  `json:"pay_url"`
	PaidAt    *string `json:"paid_at"`
	ExpiresAt *string `json:"expires_at"`
}

// GetCheckoutStatus polls the status of a checkout order on lurus-platform.
func GetCheckoutStatus(ctx context.Context, orderNo string) (*CheckoutStatus, error) {
	if IdentityServiceURL == "" {
		return nil, fmt.Errorf("identity service not configured")
	}
	url := fmt.Sprintf("%s/internal/v1/checkout/%s/status", IdentityServiceURL, orderNo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity GetCheckoutStatus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checkout status: status %d", resp.StatusCode)
	}
	var result CheckoutStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// PaymentMethod represents an available payment method from lurus-platform.
type PaymentMethod struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Type     string `json:"type"` // "qr" or "redirect"
}

// GetPaymentMethods retrieves available payment methods from lurus-platform.
func GetPaymentMethods(ctx context.Context) ([]PaymentMethod, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		IdentityServiceURL+"/internal/v1/payment-methods", nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetPaymentMethods: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var result struct {
		Methods []PaymentMethod `json:"payment_methods"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, nil
	}
	return result.Methods, nil
}

// BillingSummary holds aggregated billing information from lurus-platform.
type BillingSummary struct {
	Balance        float64 `json:"balance"`
	Frozen         float64 `json:"frozen"`
	Available      float64 `json:"available"`
	LifetimeTopup  float64 `json:"lifetime_topup"`
	LifetimeSpend  float64 `json:"lifetime_spend"`
	ActivePreAuths int64   `json:"active_pre_auths"`
	PendingOrders  int64   `json:"pending_orders"`
}

// GetBillingSummary retrieves aggregated billing info for an account from lurus-platform.
func GetBillingSummary(ctx context.Context, accountID int64) (*BillingSummary, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/billing-summary", IdentityServiceURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity GetBillingSummary: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	var bs BillingSummary
	if err := json.NewDecoder(resp.Body).Decode(&bs); err != nil {
		return nil, nil
	}
	return &bs, nil
}

// WalletTransaction mirrors entity.WalletTransaction from lurus-platform. Only
// the fields surfaced by Switch's Reseller Wallet page are decoded; the rest
// of the JSON envelope rides through as json.RawMessage on Metadata so future
// fields don't need a Switch release.
type WalletTransaction struct {
	ID            int64           `json:"id"`
	AccountID     int64           `json:"account_id"`
	Type          string          `json:"type"`
	Amount        float64         `json:"amount"`
	BalanceAfter  float64         `json:"balance_after"`
	ProductID     string          `json:"product_id"`
	ReferenceType string          `json:"reference_type"`
	ReferenceID   string          `json:"reference_id"`
	Description   string          `json:"description"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedAt     time.Time       `json:"created_at"`
}

// WalletTransactionsPage is the {data, total} envelope returned by
// InternalListWalletTransactions on lurus-platform.
type WalletTransactionsPage struct {
	Data  []WalletTransaction `json:"data"`
	Total int64               `json:"total"`
}

// ListWalletTransactionsHTTP fetches paginated wallet transactions for an account
// from lurus-platform. Returns nil, nil on transport / non-200 errors so callers
// can degrade gracefully (the Switch handler renders "服务暂不可用").
func ListWalletTransactionsHTTP(ctx context.Context, accountID int64, page, pageSize int) (*WalletTransactionsPage, error) {
	if IdentityServiceURL == "" {
		return nil, nil
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	url := fmt.Sprintf("%s/internal/v1/accounts/%d/wallet/transactions?p=%d&page_size=%d",
		IdentityServiceURL, accountID, page, pageSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)

	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity ListWalletTransactions: %v", err))
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		SysLog(fmt.Sprintf("identity ListWalletTransactions: status %d", resp.StatusCode))
		return nil, nil
	}
	var page1 WalletTransactionsPage
	if err := json.NewDecoder(resp.Body).Decode(&page1); err != nil {
		return nil, nil
	}
	return &page1, nil
}

// ReportLLMUsage sends a usage record to lurus-platform for VIP accumulation.
// Fire-and-forget — errors are logged but not propagated.
func ReportLLMUsage(ctx context.Context, accountID int64, amountCNY float64) {
	if IdentityServiceURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{
		"account_id": accountID,
		"amount_cny": amountCNY,
	})
	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		IdentityServiceURL+"/internal/v1/usage/report",
		bytes.NewReader(body),
	)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+IdentityServiceInternalKey)
	resp, err := identityClient.Do(req)
	if err != nil {
		SysLog(fmt.Sprintf("identity ReportLLMUsage: %v", err))
		return
	}
	resp.Body.Close()
}
