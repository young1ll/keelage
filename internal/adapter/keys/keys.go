// Package keys manages the daemon's ed25519 key (~/.keelage/keys, mode
// 0600) and implements port.Signer with it. Agent keys are issued under
// the owner later; v0 signs every local event with the daemon key.
package keys

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Key is an ed25519 keypair.
type Key struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

// Fingerprint is "ed25519:" + hex of sha256(pub)[:8].
func Fingerprint(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "ed25519:" + hex.EncodeToString(h[:8])
}

// Fingerprint implements port.Signer.
func (k *Key) Fingerprint() string { return Fingerprint(k.Public) }

// Sign implements port.Signer.
func (k *Key) Sign(hash []byte) ([]byte, error) {
	if len(hash) == 0 {
		return nil, errors.New("keys: nothing to sign")
	}
	return ed25519.Sign(k.Private, hash), nil
}

// Verify checks sig over hash with pub.
func Verify(pub ed25519.PublicKey, hash, sig []byte) bool {
	return len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, hash, sig)
}

// EncodePublic is the textual public key: "ed25519:" + base64.
func EncodePublic(pub ed25519.PublicKey) string {
	return "ed25519:" + base64.StdEncoding.EncodeToString(pub)
}

// DecodePublic parses EncodePublic's output.
func DecodePublic(s string) (ed25519.PublicKey, error) {
	rest, ok := strings.CutPrefix(s, "ed25519:")
	if !ok {
		return nil, errors.New("keys: public key must start with the ed25519 prefix")
	}
	b, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("keys: bad public key")
	}
	return ed25519.PublicKey(b), nil
}

// Generate creates a new keypair.
func Generate() (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Key{Private: priv, Public: pub}, nil
}

// Load reads the key at path; ok is false when it does not exist.
func Load(path string) (*Key, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, false, fmt.Errorf("keys: %s is not an ed25519 seed", path)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Key{Private: priv, Public: priv.Public().(ed25519.PublicKey)}, true, nil
}

// Save writes the seed (base64) with mode 0600 in a 0700 directory.
func Save(path string, k *Key) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k.Private.Seed())+"\n"), 0o600)
}

// LoadOrCreate returns the key at path, generating it on first use.
func LoadOrCreate(path string) (*Key, error) {
	k, ok, err := Load(path)
	if err != nil {
		return nil, err
	}
	if ok {
		return k, nil
	}
	k, err = Generate()
	if err != nil {
		return nil, err
	}
	return k, Save(path, k)
}

// Verifier implements port.SignatureVerifier over EncodePublic keys.
type Verifier struct{}

// Verify implements port.SignatureVerifier.
func (Verifier) Verify(publicKey string, hash, sig []byte) bool {
	pub, err := DecodePublic(publicKey)
	if err != nil {
		return false
	}
	return Verify(pub, hash, sig)
}

// ValidKey implements port.SignatureVerifier.
func (Verifier) ValidKey(publicKey string) bool {
	_, err := DecodePublic(publicKey)
	return err == nil
}
