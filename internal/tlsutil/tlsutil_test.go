package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestSelfSigned(t *testing.T) {
	dir := t.TempDir()
	cert, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := SelfSigned(cert, key, []string{"localhost", "nas", "192.168.1.5"}); err != nil {
		t.Fatal(err)
	}
	if err := Check(cert, key); err != nil {
		t.Fatalf("pair unusable: %v", err)
	}
	data, _ := os.ReadFile(cert)
	block, _ := pem.Decode(data)
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.VerifyHostname("192.168.1.5"); err != nil {
		t.Errorf("IP SAN missing: %v", err)
	}
	if err := c.VerifyHostname("nas"); err != nil {
		t.Errorf("DNS SAN missing: %v", err)
	}
}
