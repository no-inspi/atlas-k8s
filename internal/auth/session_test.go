package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealRoundTrip(t *testing.T) {
	s, err := NewSealer(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	in := Session{User: "alice@example.com", Groups: []string{"oidc:sre"}}
	v, err := s.Seal("atlas_session", in)
	if err != nil {
		t.Fatal(err)
	}
	var out Session
	if err := s.Open("atlas_session", v, &out); err != nil {
		t.Fatal(err)
	}
	if out.User != in.User || len(out.Groups) != 1 || out.Groups[0] != "oidc:sre" {
		t.Errorf("session = %+v", out)
	}
	if strings.Contains(v, "alice") {
		t.Error("le cookie ne doit pas contenir l'utilisateur en clair")
	}
}

func TestOpenRejectsTampering(t *testing.T) {
	s, _ := NewSealer(testKey(1))
	other, _ := NewSealer(testKey(2))
	v, _ := s.Seal("atlas_session", Session{User: "alice"})
	var out Session

	b := []byte(v)
	b[len(b)-3] ^= 1
	if s.Open("atlas_session", string(b), &out) == nil {
		t.Error("cookie modifié accepté")
	}
	if other.Open("atlas_session", v, &out) == nil {
		t.Error("cookie accepté avec une autre clé")
	}
	if s.Open("atlas_oauth", v, &out) == nil {
		t.Error("cookie accepté sous un autre nom")
	}
	if s.Open("atlas_session", "", &out) == nil || s.Open("atlas_session", "%%%", &out) == nil {
		t.Error("valeurs invalides acceptées")
	}
}

func TestSealRejectsHugeCookies(t *testing.T) {
	s, _ := NewSealer(testKey(1))
	groups := make([]string, 400)
	for i := range groups {
		groups[i] = "oidc:a-very-long-group-name-from-the-identity-provider"
	}
	if _, err := s.Seal("atlas_session", Session{User: "x", Groups: groups}); err == nil {
		t.Error("un cookie de plus de 4 Ko doit être refusé")
	}
}

func TestParseKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(testKey(7))
	if k, err := ParseKey(good); err != nil || len(k) != 32 {
		t.Errorf("clé valide refusée : %v", err)
	}
	for _, bad := range []string{"", "pas-du-base64", base64.StdEncoding.EncodeToString([]byte("trop courte"))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("clé %q acceptée", bad)
		}
	}
}
