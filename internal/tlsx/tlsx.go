// Package tlsx generates and persists a self-signed certificate for the
// gcp-local emulator so SDK clients that hard-code https:// can talk to it
// without WithoutAuthentication() workarounds. Stdlib-only.
package tlsx

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Paths describes where the cert and key live on disk.
type Paths struct {
	Dir      string
	CertFile string
	KeyFile  string
}

// DefaultPaths returns ~/.gcp-local/tls/{cert,key}.pem.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("user home dir: %w", err)
	}
	dir := filepath.Join(home, ".gcp-local", "tls")
	return Paths{
		Dir:      dir,
		CertFile: filepath.Join(dir, "cert.pem"),
		KeyFile:  filepath.Join(dir, "key.pem"),
	}, nil
}

// EnsureCert returns the cert/key paths, generating them if missing. The
// generated cert is valid for localhost, 127.0.0.1, and ::1. If both files
// already exist the function is a no-op so the cert persists across restarts.
func EnsureCert(p Paths) error {
	if exists(p.CertFile) && exists(p.KeyFile) {
		return nil
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", p.Dir, err)
	}
	return generate(p)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func generate(p Paths) error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate rsa key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("serial: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour)

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"gcp-local"},
			CommonName:   "gcp-local self-signed",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}

	if err := writePEM(p.CertFile, 0o644, "CERTIFICATE", der); err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal key: %w", err)
	}
	if err := writePEM(p.KeyFile, 0o600, "PRIVATE KEY", keyDER); err != nil {
		// Best-effort cleanup so we don't leave a half-written pair behind.
		_ = os.Remove(p.CertFile)
		return err
	}
	return nil
}

func writePEM(path string, mode os.FileMode, blockType string, der []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return fmt.Errorf("encode pem %s: %w", path, err)
	}
	return nil
}
