package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// loadOrCreateCert loads the certificate and key at certFile and keyFile. If neither file
// exists, it creates a self-signed certificate for hosts and writes both files. created tells
// which happened.
func loadOrCreateCert(certFile, keyFile string, hosts []string) (cert tls.Certificate, created bool, err error) {
	_, errC := os.Stat(certFile)
	_, errK := os.Stat(keyFile)
	switch {
	case errC == nil && errK == nil:
		cert, err = tls.LoadX509KeyPair(certFile, keyFile)
		return cert, false, err
	case !errors.Is(errC, fs.ErrNotExist) || !errors.Is(errK, fs.ErrNotExist):
		return cert, false, fmt.Errorf("need both %s and %s, or neither to create a self-signed certificate", certFile, keyFile)
	}
	if err := writeSelfSigned(certFile, keyFile, hosts); err != nil {
		return cert, false, err
	}
	cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	return cert, true, err
}

func writeSelfSigned(certFile, keyFile string, hosts []string) error {
	// ECDSA P-256, because browsers (the stage) support it everywhere.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "agentic office hub (self-signed)"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	for _, f := range []string{certFile, keyFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// fingerprint returns the SHA-256 fingerprint of the leaf certificate, for pinning by hand.
func fingerprint(cert tls.Certificate) string {
	if len(cert.Certificate) == 0 {
		return ""
	}
	h := sha256.Sum256(cert.Certificate[0])
	return hex.EncodeToString(h[:])
}
