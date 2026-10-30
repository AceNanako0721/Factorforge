package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func makeTLS(root string) error {
	dir := filepath.Join(root, "tls")
	complete := filepath.Join(dir, ".complete")
	if _, e := os.Stat(complete); e == nil {
		for _, name := range []string{"trust/ca.pem", "gateway/cert.pem", "gateway/key.pem", "console/cert.pem", "console/key.pem"} {
			if _, e = os.Stat(filepath.Join(dir, name)); e != nil {
				return fail("TLS_ASSETS_INCOMPLETE")
			}
		}
		return nil
	}
	if _, e := os.Stat(filepath.Join(dir, "trust/ca.pem")); e == nil {
		return fail("TLS_PARTIAL_REQUIRES_REVIEW")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)); return n }
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Factorforge private deployment CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		return e
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		return e
	}
	if e = write(filepath.Join(dir, "trust/ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); e != nil {
		return e
	}
	raw, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return e
	}
	if e = write(filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})); e != nil {
		return e
	}
	for _, name := range []string{"gateway", "console"} {
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return e
		}
		cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name, "localhost", "console-api"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 1, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		leaf, e := x509.CreateCertificate(rand.Reader, cert, parsed, &k.PublicKey, key)
		if e != nil {
			return e
		}
		if e = write(filepath.Join(dir, name, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf})); e != nil {
			return e
		}
		kb, _ := x509.MarshalECPrivateKey(k)
		if e = write(filepath.Join(dir, name, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})); e != nil {
			return e
		}
	}
	return write(complete, []byte("Certificates generated; host trust requires explicit installation\n"))
}
