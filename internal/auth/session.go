// Package auth gère la connexion OIDC (code + PKCE), la session en cookie
// chiffré et la protection des routes.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Session est l'identité de l'utilisateur, telle qu'envoyée en impersonation.
type Session struct {
	User    string    `json:"u"`
	Groups  []string  `json:"g,omitempty"`
	Expires time.Time `json:"e"`
}

// maxCookieSize laisse de la marge sous la limite de 4 Ko des navigateurs.
const maxCookieSize = 3800

// Sealer chiffre et authentifie les cookies (AES-256-GCM). Le nom du cookie est
// lié au chiffré : un cookie ne peut pas être rejoué sous un autre nom.
type Sealer struct{ aead cipher.AEAD }

// ParseKey décode la clé ATLAS_COOKIE_KEY (base64 de 32 octets).
func ParseKey(b64 string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("clé de cookie : base64 invalide : %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("clé de cookie : 32 octets attendus, %d reçus", len(k))
	}
	return k, nil
}

func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, errors.New("clé de cookie : 32 octets attendus")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(name string, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, []byte(name)))
	if len(out) > maxCookieSize {
		return "", fmt.Errorf("session trop volumineuse (%d octets) : trop de groupes ; réduisez le claim de groupes côté IdP", len(out))
	}
	return out, nil
}

func (s *Sealer) Open(name, value string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return errors.New("cookie illisible")
	}
	n := s.aead.NonceSize()
	if len(raw) < n {
		return errors.New("cookie trop court")
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(name))
	if err != nil {
		return errors.New("cookie invalide")
	}
	return json.Unmarshal(plain, v)
}
