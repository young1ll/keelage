package keys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keys", "daemon.ed25519")
	k, err := LoadOrCreate(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", fi.Mode().Perm())
	}
	again, err := LoadOrCreate(p)
	if err != nil || again.Fingerprint() != k.Fingerprint() {
		t.Fatal("must load the same key")
	}
	hash := []byte("0123456789abcdef0123456789abcdef")
	sig, err := k.Sign(hash)
	if err != nil || !Verify(k.Public, hash, sig) {
		t.Fatal("sign/verify")
	}
	if Verify(k.Public, []byte("other"), sig) {
		t.Fatal("wrong hash must not verify")
	}
	pub, err := DecodePublic(EncodePublic(k.Public))
	if err != nil || !Verify(pub, hash, sig) {
		t.Fatal("public key round trip")
	}
	if _, err := DecodePublic("rsa:xx"); err == nil {
		t.Fatal("bad prefix")
	}
	if _, err := k.Sign(nil); err == nil {
		t.Fatal("empty hash")
	}
	_ = os.WriteFile(p, []byte("garbage"), 0o600)
	if _, _, err := Load(p); err == nil {
		t.Fatal("corrupt seed must error")
	}
}
