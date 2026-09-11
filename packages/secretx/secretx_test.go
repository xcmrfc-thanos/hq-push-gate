package secretx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func testRing(t *testing.T, kid string) *KeyRing {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	r, err := LoadKeysWithMap(map[string]string{"HQ_MASTER_KEYS": kid + "=" + hex.EncodeToString(raw)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	r := testRing(t, "k1")
	token, err := r.Encrypt("sk-test-123456")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "v1:k1:") {
		t.Fatalf("bad prefix: %s", token)
	}
	got, err := r.Decrypt(token)
	if err != nil || got != "sk-test-123456" {
		t.Fatalf("roundtrip: %q %v", got, err)
	}
}

func TestRotationTwoKeys(t *testing.T) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	old, err := LoadKeysWithMap(map[string]string{"HQ_MASTER_KEYS": "k0=" + hex.EncodeToString(raw)})
	if err != nil {
		t.Fatal(err)
	}
	cur := testRing(t, "k1")
	both := &KeyRing{Keys: append(cur.Keys, old.Keys...)} // k1 当前，k0 仅解密

	oldToken, _ := old.Encrypt("legacy")
	if got, err := both.Decrypt(oldToken); err != nil || got != "legacy" {
		t.Fatalf("old key decrypt: %q %v", got, err)
	}
	newToken, _ := both.Encrypt("fresh")
	if !strings.HasPrefix(newToken, "v1:k1:") {
		t.Fatalf("encrypt must use first key: %s", newToken)
	}
}

func TestTamperRejected(t *testing.T) {
	r := testRing(t, "k1")
	token, _ := r.Encrypt("secret")
	bad := token[:len(token)-4] + "AAAA"
	if _, err := r.Decrypt(bad); err == nil {
		t.Fatal("tampered token must fail")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	token, _ := testRing(t, "k1").Encrypt("s")
	if _, err := testRing(t, "k1").Decrypt(token); err == nil {
		t.Fatal("different key must fail")
	}
}

func TestKidBinding(t *testing.T) {
	r := testRing(t, "k1")
	token, _ := r.Encrypt("s")
	forged := strings.Replace(token, ":k1:", ":k2:", 1)
	both := &KeyRing{Keys: append(r.Keys, testRing(t, "k2").Keys...)}
	if _, err := both.Decrypt(forged); err == nil {
		t.Fatal("kid-forged token must fail (AAD binding)")
	}
}

func TestLoadKeysCompatAndValidation(t *testing.T) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	hx := hex.EncodeToString(raw)

	r, err := LoadKeysWithMap(map[string]string{"HQ_MASTER_KEY": hx})
	if err != nil || r.Keys[0].Kid != "k1" {
		t.Fatalf("legacy env: %v %v", r, err)
	}
	r2, err := LoadKeysWithMap(map[string]string{"HQ_MASTER_KEYS": "k2=" + hx})
	if err != nil || r2.Keys[0].Kid != "k2" {
		t.Fatalf("named env: %v %v", r2, err)
	}
	if _, err := LoadKeysWithMap(map[string]string{}); err == nil {
		t.Fatal("missing env must fail")
	}
	if _, err := LoadKeysWithMap(map[string]string{"HQ_MASTER_KEY": "zz"}); err == nil {
		t.Fatal("non-hex must fail")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("sk-1234567890abcdef"); got != "sk-***cdef" {
		t.Fatalf("mask: %q", got)
	}
	if got := MaskSecret("short"); got != "***" {
		t.Fatalf("mask short: %q", got)
	}
}

func TestValidateBaseURL(t *testing.T) {
	ok := "https://ark.cn-beijing.volces.com/api/coding/v3/"
	got, err := ValidateBaseURL(ok)
	if err != nil || got != "https://ark.cn-beijing.volces.com/api/coding/v3" {
		t.Fatalf("validate: %q %v", got, err)
	}
	if _, err := ValidateBaseURL("https://8.8.8.8/v1"); err != nil {
		t.Fatalf("public ip must pass: %v", err)
	}
	for _, bad := range []string{
		"ftp://x.com", "not-a-url", "",
		"http://localhost:8080/v1", "https://box.local/v1",
		"https://127.0.0.1/v1", "https://10.1.2.3", "https://192.168.1.1", "https://169.254.1.1",
	} {
		if _, err := ValidateBaseURL(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
}
