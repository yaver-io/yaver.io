package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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

// tlsDir returns the directory for TLS certs (~/.yaver/tls/).
func tlsDir() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "yaver", "tls")
}

// EnsureTLSCert generates a self-signed TLS certificate if one doesn't exist.
// The cert includes SANs for all local IPs, localhost, and 127.0.0.1.
// Returns the cert, its SHA256 fingerprint, and any error.
func EnsureTLSCert() (tls.Certificate, string, error) {
	dir := tlsDir()
	certPath := filepath.Join(dir, "server.pem")
	keyPath := filepath.Join(dir, "server-key.pem")

	// Check if cert already exists
	if _, err := os.Stat(certPath); err == nil {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err == nil {
			fp, err := certFingerprint(certPath)
			if err == nil {
				return cert, fp, nil
			}
		}
		// Corrupt or unreadable — regenerate
	}

	// Generate new cert
	return generateTLSCert(dir, certPath, keyPath)
}

func generateTLSCert(dir, certPath, keyPath string) (tls.Certificate, string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create tls dir: %w", err)
	}

	// Generate ECDSA key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generate key: %w", err)
	}

	// Serial number
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))

	// Collect all local IPs for SANs
	ips := collectLocalIPs()
	ips = append(ips, net.ParseIP("127.0.0.1"), net.ParseIP("::1"))

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Yaver Agent"},
			CommonName:   "yaver-agent",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(2 * 365 * 24 * time.Hour), // 2 years
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create certificate: %w", err)
	}

	// Write cert
	certFile, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write cert: %w", err)
	}
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	// Write key
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("marshal key: %w", err)
	}
	keyFile, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write key: %w", err)
	}
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()

	// Load as tls.Certificate
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("load cert: %w", err)
	}

	fp := fmt.Sprintf("%x", sha256.Sum256(certDER))
	return cert, fp, nil
}

func certFingerprint(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("no PEM block found")
	}
	fp := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("%x", fp), nil
}

func collectLocalIPs() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}
