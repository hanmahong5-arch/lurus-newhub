package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func withIdentityServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	prevURL, prevKey := IdentityServiceURL, IdentityServiceInternalKey
	IdentityServiceURL, IdentityServiceInternalKey = srv.URL, "k"
	t.Cleanup(func() {
		srv.Close()
		IdentityServiceURL, IdentityServiceInternalKey = prevURL, prevKey
	})
	return srv
}

func TestLookupAccountByIDPSubject_Outcomes(t *testing.T) {
	var gotPath, gotAuth atomic.Value
	status := int32(200)
	var body atomic.Value
	body.Store(`{"id":7,"idp_subject":"s","is_platform_admin":true}`)
	withIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.EscapedPath())
		gotAuth.Store(r.Header.Get("Authorization"))
		st := atomic.LoadInt32(&status)
		w.WriteHeader(int(st))
		if st == 200 {
			_, _ = w.Write([]byte(body.Load().(string)))
		}
	})

	a, err := LookupAccountByIDPSubject(context.Background(), "a/b c")
	if err != nil || a == nil || !a.IsPlatformAdmin || a.ID != 7 {
		t.Fatalf("200: got %+v, %v", a, err)
	}
	if gotPath.Load() != "/internal/v1/accounts/by-idp-sub/a%2Fb%20c" {
		t.Errorf("sub not path-escaped: %v", gotPath.Load())
	}
	if gotAuth.Load() != "Bearer k" {
		t.Errorf("auth header = %v", gotAuth.Load())
	}

	atomic.StoreInt32(&status, 404)
	if a, err := LookupAccountByIDPSubject(context.Background(), "x"); a != nil || err != nil {
		t.Errorf("404: got %+v, %v want nil,nil", a, err)
	}
	atomic.StoreInt32(&status, 500)
	if a, err := LookupAccountByIDPSubject(context.Background(), "x"); a != nil || !errors.Is(err, ErrIdentityUnavailable) {
		t.Errorf("500: got %+v, %v", a, err)
	}
	atomic.StoreInt32(&status, 200)
	body.Store(`{not json`)
	if _, err := LookupAccountByIDPSubject(context.Background(), "x"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Errorf("bad body: %v", err)
	}
}

func TestLookupAccountByIDPSubject_NetworkAndUnconfigured(t *testing.T) {
	srv := withIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {})
	srv.Close()
	if _, err := LookupAccountByIDPSubject(context.Background(), "x"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Errorf("closed server: %v", err)
	}
	IdentityServiceURL = ""
	if _, err := LookupAccountByIDPSubject(context.Background(), "x"); !errors.Is(err, ErrIdentityNotConfigured) {
		t.Errorf("empty url: %v", err)
	}
}

// Regression: the legacy wrapper keeps swallowing every error.
func TestGetAccountByZitadelSub_StillSwallowsErrors(t *testing.T) {
	status := int32(500)
	srv := withIdentityServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(atomic.LoadInt32(&status)))
	})
	if a, err := GetAccountByZitadelSub(context.Background(), "x"); a != nil || err != nil {
		t.Errorf("500: got %+v, %v want nil,nil", a, err)
	}
	atomic.StoreInt32(&status, 404)
	if a, err := GetAccountByZitadelSub(context.Background(), "x"); a != nil || err != nil {
		t.Errorf("404: got %+v, %v", a, err)
	}
	srv.Close()
	if a, err := GetAccountByZitadelSub(context.Background(), "x"); a != nil || err != nil {
		t.Errorf("closed: got %+v, %v", a, err)
	}
	IdentityServiceURL = ""
	if a, err := GetAccountByZitadelSub(context.Background(), "x"); a != nil || err != nil {
		t.Errorf("empty: got %+v, %v", a, err)
	}
}
