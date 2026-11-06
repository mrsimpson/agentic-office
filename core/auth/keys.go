// Package auth holds the client side of identity (concept-identity, dec-auth-keypair) that all
// participant clients share: the CLI, the MCP server and the ears.
//
//   - Key pairs: create, write and read the local Ed25519 key of an actor. Only the public key
//     leaves the device.
//   - Challenge and response: sign a challenge of the server (SignChallenge) and keep a
//     short-lived access token fresh (TokenSource).
//   - Connect calls: attach the token to every call (Bearer, StaticToken).
//   - TLS: trust the certificate of the office server (TLSConfig, HTTPClient).
//
// Text forms:
//
//   - A public key in text is the standard base64 form of its 32 raw bytes, for example in the
//     admin file of the server.
//   - A key ID is "SHA256:" followed by the unpadded standard base64 form of the SHA-256 hash of
//     the raw public key (the same style as OpenSSH fingerprints). The server returns it from
//     RegisterKey, and anyone can compute it from the public key with KeyID.
//   - A private key file is PEM ("PRIVATE KEY", PKCS #8), readable only by its owner.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NewKey creates a new Ed25519 key pair.
func NewKey() (ed25519.PrivateKey, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	return key, err
}

// WriteKey writes key to path as PEM with mode 0600. It does not overwrite an existing file.
func WriteKey(path string, key ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadKey reads a private key that WriteKey wrote. It refuses a file that others can read.
func ReadKey(path string) (ed25519.PrivateKey, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: private key is readable by others (mode %v); run chmod 600", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s: no PEM private key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s: not an Ed25519 key", path)
	}
	return key, nil
}

// LoadOrCreateKey reads the key at path. If there is no file, it creates a new key and writes it.
// created tells which happened.
func LoadOrCreateKey(path string) (key ed25519.PrivateKey, created bool, err error) {
	key, err = ReadKey(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return key, false, err
	}
	if key, err = NewKey(); err != nil {
		return nil, false, err
	}
	if err := WriteKey(path, key); err != nil {
		return nil, false, err
	}
	return key, true, nil
}

// PublicKey returns the public half of key.
func PublicKey(key ed25519.PrivateKey) ed25519.PublicKey { return key.Public().(ed25519.PublicKey) }

// FormatPublicKey returns the text form of a public key.
func FormatPublicKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

// ParsePublicKey reads the text form of a public key.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key: %d bytes, want %d", len(b), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// KeyID returns the key ID of a public key.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(h[:])
}

// ChallengePrefix starts every challenge of the server. A client signs only data that starts with
// it, so a signature for the office can never be used as a signature for something else.
const ChallengePrefix = "agentic-office auth v1\n"

// SignChallenge signs a challenge of the server. It fails if the challenge does not start with
// ChallengePrefix.
func SignChallenge(key ed25519.PrivateKey, challenge []byte) ([]byte, error) {
	if !strings.HasPrefix(string(challenge), ChallengePrefix) {
		return nil, errors.New("challenge does not start with the office prefix; refusing to sign it")
	}
	return ed25519.Sign(key, challenge), nil
}
