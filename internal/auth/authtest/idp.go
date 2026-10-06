// Package authtest fournit un fournisseur OIDC de test (discovery, JWKS,
// endpoint token avec vérification PKCE, ID tokens signés RS256).
package authtest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// IdP est un fournisseur OIDC minimal : discovery, JWKS et endpoint token
// qui vérifie le code_verifier PKCE avant de délivrer un ID token signé.
type IdP struct {
	URL      string
	ClientID string
	Secret   string
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	codes    map[string]pendingCode
}

type pendingCode struct {
	claims    map[string]any
	challenge string
}

func NewIdP(t testing.TB) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &IdP{key: key, codes: map[string]pendingCode{}, ClientID: "atlas", Secret: "s3cret"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                idp.srv.URL,
			"authorization_endpoint":                idp.srv.URL + "/authorize",
			"token_endpoint":                        idp.srv.URL + "/token",
			"jwks_uri":                              idp.srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if u, p, _ := r.BasicAuth(); u != idp.ClientID || p != idp.Secret {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		idp.mu.Lock()
		pc, ok := idp.codes[r.Form.Get("code")]
		delete(idp.codes, r.Form.Get("code"))
		idp.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pc.challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idp.sign(t, pc.claims),
		})
	})
	idp.srv = httptest.NewServer(mux)
	idp.URL = idp.srv.URL
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *IdP) sign(t testing.TB, claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: idp.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]any{"iss": idp.srv.URL, "aud": idp.ClientID, "sub": "user-1",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	for k, v := range claims {
		full[k] = v
	}
	payload, _ := json.Marshal(full)
	jws, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := jws.CompactSerialize()
	return s
}

// IssueCode prépare un code d'autorisation pour le challenge PKCE donné ;
// le token endpoint l'échangera contre un ID token portant ces claims.
func (idp *IdP) IssueCode(code, challenge string, claims map[string]any) {
	idp.mu.Lock()
	idp.codes[code] = pendingCode{claims: claims, challenge: challenge}
	idp.mu.Unlock()
}
