package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Persistent relay key + SPKI pin.
//
// The relay used to mint a fresh ECDSA key on every boot (generateRelayTLS), so
// its SubjectPublicKeyInfo changed each restart. That made agent-side pinning
// impossible: there was nothing stable to pin to. This loads a keypair from disk
// (YAVER_RELAY_KEY_PATH, default /opt/yaver-relay/relay-key.pem) and generates +
// saves one only if absent — so the SPKI is stable across restarts, and agents
// can pin it (see desktop/agent/relay_pinning.go, gated on platformConfig
// spki_pin).
//
// The private key is a secret and lives only on the relay box at 0600. The SPKI
// PIN it yields is derived from the PUBLIC key and is safe to log and publish —
// that is the whole point of a pin.

func relayKeyPath() string {
	if p := os.Getenv("YAVER_RELAY_KEY_PATH"); p != "" {
		return p
	}
	return "/opt/yaver-relay/relay-key.pem"
}

// loadOrCreateRelayKey returns a stable ECDSA P-256 key, generating and
// persisting one on first run. Falls back to an in-memory ephemeral key (with a
// warning) if the path is unwritable, so a read-only or misconfigured box still
// serves TLS — just without a pinnable identity.
func loadOrCreateRelayKey() (*ecdsa.PrivateKey, bool) {
	path := relayKeyPath()
	if data, err := os.ReadFile(path); err == nil {
		if block, _ := pem.Decode(data); block != nil {
			if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return key, true
			}
		}
		log.Printf("[RELAY] key file %s unparseable — regenerating", path)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, false
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return key, false
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("[RELAY] cannot create key dir (%v) — using an EPHEMERAL key; SPKI will change on restart and pinning cannot be used", err)
		return key, false
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		log.Printf("[RELAY] cannot persist key (%v) — using an EPHEMERAL key; SPKI will change on restart and pinning cannot be used", err)
		return key, false
	}
	log.Printf("[RELAY] generated and persisted a new relay key at %s", path)
	return key, true
}

// spkiPin is the base64 SHA-256 of the cert's SubjectPublicKeyInfo — the value
// agents pin against. Public, safe to log and publish.
func spkiPin(certDER []byte) string {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// generatePersistentRelayTLS builds the QUIC TLS config from the persistent key
// and logs the SPKI pin so an operator can publish it to platformConfig. Drop-in
// replacement for generateRelayTLS.
func generatePersistentRelayTLS() (*tls.Config, error) {
	priv, persistent := loadOrCreateRelayKey()

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"Yaver Relay"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	if pin := spkiPin(certDER); pin != "" {
		if persistent {
			log.Printf("[RELAY] SPKI pin (publish this as spki_pin in platformConfig relay_servers): %s", pin)
		} else {
			log.Printf("[RELAY] SPKI pin (EPHEMERAL — changes on restart, do NOT pin): %s", pin)
		}
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  priv,
		}},
		NextProtos: []string{"yaver-relay"},
	}, nil
}
