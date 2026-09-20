package handler

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/adapter/repo"
)

// TestRedemptionFailureStatus pins the split between "the caller got it
// wrong" (userStatus, whatever the endpoint's contract says) and "the hub
// broke" (500). Mutation that must turn this red: make the function return
// userStatus unconditionally — the FAILED rows below then read 400/200.
func TestRedemptionFailureStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		userStatus int
		want       int
	}{
		{"invalid keeps the endpoint's user status (400)", repo.ErrRedemptionInvalid, http.StatusBadRequest, http.StatusBadRequest},
		{"used keeps the endpoint's user status (200 envelope)", repo.ErrRedemptionUsed, http.StatusOK, http.StatusOK},
		{"expired keeps the endpoint's user status", repo.ErrRedemptionExpired, http.StatusBadRequest, http.StatusBadRequest},
		{"wrong tenant keeps the endpoint's user status", repo.ErrRedemptionWrongTenant, http.StatusBadRequest, http.StatusBadRequest},
		{"wrapped user error still reads as a user error", fmt.Errorf("redeem: %w", repo.ErrRedemptionUsed), http.StatusBadRequest, http.StatusBadRequest},
		{"the FAILED sentinel is a 500 on a 400 endpoint", repo.ErrRedemptionFailed, http.StatusBadRequest, http.StatusInternalServerError},
		{"the FAILED sentinel is a 500 on a 200-envelope endpoint", repo.ErrRedemptionFailed, http.StatusOK, http.StatusInternalServerError},
		{"a raw error that escaped Redeem's tail is a 500 too", errors.New("pq: column quota does not exist"), http.StatusBadRequest, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redemptionFailureStatus(tc.err, tc.userStatus); got != tc.want {
				t.Fatalf("redemptionFailureStatus(%v, %d) = %d, want %d", tc.err, tc.userStatus, got, tc.want)
			}
		})
	}
}
