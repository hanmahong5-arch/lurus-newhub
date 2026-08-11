package vertex

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/LurusTech/lurus-hub/internal/pkg/dto"
	"github.com/golang-jwt/jwt/v5"
)

// ---------------------------------------------------------------------------
// GetModelRegion: routes a request to the correct GCP region. A wrong region
// means the request 404s upstream or silently hits the wrong data-residency
// zone.
// ---------------------------------------------------------------------------

func TestGetModelRegion(t *testing.T) {
	tests := []struct {
		name           string
		apiVersion     string
		localModelName string
		want           string
	}{
		{"plain region string passed through verbatim", "us-central1", "gemini-1.5-pro", "us-central1"},
		{"JSON map picks the model-specific region", `{"gemini-1.5-pro":"europe-west1","default":"us-central1"}`, "gemini-1.5-pro", "europe-west1"},
		{"JSON map falls back to default when model absent", `{"other-model":"europe-west1","default":"us-central1"}`, "gemini-1.5-pro", "us-central1"},
		{"JSON map with neither model nor default falls back to global", `{"other-model":"europe-west1"}`, "gemini-1.5-pro", "global"},
		{"empty string is not a JSON object, returned as-is", "", "gemini-1.5-pro", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetModelRegion(tt.apiVersion, tt.localModelName)
			if got != tt.want {
				t.Errorf("GetModelRegion(%q, %q) = %q, want %q", tt.apiVersion, tt.localModelName, got, tt.want)
			}
		})
	}

	t.Run("malformed JSON object string falls back to the original string", func(t *testing.T) {
		malformed := `{"not":"closed`
		got := GetModelRegion(malformed, "gemini-1.5-pro")
		if got != malformed {
			t.Errorf("GetModelRegion(malformed) = %q, want the original string returned on parse failure", got)
		}
	})
}

// ---------------------------------------------------------------------------
// removeFunctionResponseID: Vertex AI rejects functionResponse.id, so this
// mutation must strip it everywhere -- including nested batch (Requests) --
// without touching unrelated parts or panicking on nil/empty input.
// ---------------------------------------------------------------------------

func TestRemoveFunctionResponseID(t *testing.T) {
	t.Run("nil request does not panic", func(t *testing.T) {
		removeFunctionResponseID(nil)
	})

	t.Run("strips id from a top-level functionResponse part", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{Parts: []dto.GeminiPart{
					{FunctionResponse: &dto.GeminiFunctionResponse{Name: "f", ID: []byte(`"call-1"`)}},
				}},
			},
		}
		removeFunctionResponseID(req)
		if req.Contents[0].Parts[0].FunctionResponse.ID != nil {
			t.Errorf("ID = %s, want nil after stripping", req.Contents[0].Parts[0].FunctionResponse.ID)
		}
		if req.Contents[0].Parts[0].FunctionResponse.Name != "f" {
			t.Errorf("Name = %q, want untouched %q", req.Contents[0].Parts[0].FunctionResponse.Name, "f")
		}
	})

	t.Run("parts without a functionResponse are left untouched", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{Parts: []dto.GeminiPart{{Text: "hello"}}},
			},
		}
		removeFunctionResponseID(req)
		if req.Contents[0].Parts[0].Text != "hello" {
			t.Errorf("Text = %q, want untouched", req.Contents[0].Parts[0].Text)
		}
	})

	t.Run("recurses into nested batch Requests", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Requests: []dto.GeminiChatRequest{
				{
					Contents: []dto.GeminiChatContent{
						{Parts: []dto.GeminiPart{
							{FunctionResponse: &dto.GeminiFunctionResponse{ID: []byte(`"nested-id"`)}},
						}},
					},
				},
			},
		}
		removeFunctionResponseID(req)
		if req.Requests[0].Contents[0].Parts[0].FunctionResponse.ID != nil {
			t.Errorf("nested ID = %s, want stripped by recursion", req.Requests[0].Contents[0].Parts[0].FunctionResponse.ID)
		}
	})

	t.Run("empty ID is left as nil, not reset to a non-nil empty value", func(t *testing.T) {
		req := &dto.GeminiChatRequest{
			Contents: []dto.GeminiChatContent{
				{Parts: []dto.GeminiPart{
					{FunctionResponse: &dto.GeminiFunctionResponse{ID: nil}},
				}},
			},
		}
		removeFunctionResponseID(req)
		if req.Contents[0].Parts[0].FunctionResponse.ID != nil {
			t.Errorf("ID = %v, want still nil", req.Contents[0].Parts[0].FunctionResponse.ID)
		}
	})
}

// ---------------------------------------------------------------------------
// createSignedJWT: the auth seam for service-account credentials. A bad key
// must fail loudly, not silently produce an unusable/garbage token that
// upstream Google rejects with an opaque 401.
// ---------------------------------------------------------------------------

func TestCreateSignedJWT(t *testing.T) {
	t.Run("malformed PEM errors", func(t *testing.T) {
		_, err := createSignedJWT("sa@example.iam.gserviceaccount.com", "not-a-valid-pem-body")
		if err == nil {
			t.Fatal("expected error for malformed PEM, got nil")
		}
	})

	t.Run("non-RSA key (ECDSA) is rejected", func(t *testing.T) {
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(ecKey)
		if err != nil {
			t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
		}
		pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		// strip the header/footer the way createSignedJWT expects to add them back
		body := strings.ReplaceAll(string(pemBlock), "-----BEGIN PRIVATE KEY-----", "")
		body = strings.ReplaceAll(body, "-----END PRIVATE KEY-----", "")
		_, err = createSignedJWT("sa@example.iam.gserviceaccount.com", body)
		if err == nil {
			t.Fatal("expected error for non-RSA key, got nil")
		}
		if !strings.Contains(err.Error(), "not an RSA private key") {
			t.Errorf("error = %v, want 'not an RSA private key'", err)
		}
	})

	t.Run("valid RSA key produces a JWT with the expected claims", func(t *testing.T) {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
		if err != nil {
			t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
		}
		pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		body := strings.ReplaceAll(string(pemBlock), "-----BEGIN PRIVATE KEY-----", "")
		body = strings.ReplaceAll(body, "-----END PRIVATE KEY-----", "")

		signed, err := createSignedJWT("sa@example.iam.gserviceaccount.com", body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		parsed, _, err := jwt.NewParser().ParseUnverified(signed, jwt.MapClaims{})
		if err != nil {
			t.Fatalf("failed to parse produced JWT: %v", err)
		}
		claims, ok := parsed.Claims.(jwt.MapClaims)
		if !ok {
			t.Fatalf("claims type = %T, want jwt.MapClaims", parsed.Claims)
		}
		if claims["iss"] != "sa@example.iam.gserviceaccount.com" {
			t.Errorf("iss claim = %v, want the service account email", claims["iss"])
		}
		if claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" {
			t.Errorf("scope claim = %v, want cloud-platform scope", claims["scope"])
		}
		if claims["aud"] != "https://www.googleapis.com/oauth2/v4/token" {
			t.Errorf("aud claim = %v, want the token endpoint", claims["aud"])
		}
	})

	t.Run("embedded literal \\n sequences in the key are stripped before parsing", func(t *testing.T) {
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		der, err := x509.MarshalPKCS8PrivateKey(rsaKey)
		if err != nil {
			t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
		}
		pemBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
		body := strings.ReplaceAll(string(pemBlock), "-----BEGIN PRIVATE KEY-----", "")
		body = strings.ReplaceAll(body, "-----END PRIVATE KEY-----", "")
		// Simulate the common env-var encoding where real newlines were escaped as \n.
		escaped := strings.ReplaceAll(strings.TrimSpace(body), "\n", "\\n")

		_, err = createSignedJWT("sa@example.iam.gserviceaccount.com", escaped)
		if err != nil {
			t.Fatalf("unexpected error for \\n-escaped key: %v", err)
		}
	})
}
