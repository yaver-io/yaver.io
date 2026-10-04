package e2ee

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// DeviceAuthorization is created by an already-trusted endpoint after an
// out-of-band QR/fingerprint check. The control plane may store and route this
// certificate but cannot change or create membership without that endpoint.
type DeviceAuthorization struct {
	Version                 int    `json:"version"`
	AccountID               string `json:"accountId"`
	AuthorizerDeviceID      string `json:"authorizerDeviceId"`
	SubjectDeviceID         string `json:"subjectDeviceId"`
	SubjectIdentityPublic   string `json:"subjectIdentityPublicKey"`
	SubjectEncryptionPublic string `json:"subjectEncryptionPublicKey"`
	PairingNonce            string `json:"pairingNonce"`
	IssuedAtMS              int64  `json:"issuedAtMs"`
	ExpiresAtMS             int64  `json:"expiresAtMs"`
	Signature               string `json:"signature"`
}

func deviceAuthorizationCanonical(c DeviceAuthorization) []byte {
	return []byte(fmt.Sprintf(
		"yaver-device-authorization-v1\n%d\n%s\n%s\n%s\n%s\n%s\n%s\n%d\n%d",
		c.Version, c.AccountID, c.AuthorizerDeviceID, c.SubjectDeviceID,
		c.SubjectIdentityPublic, c.SubjectEncryptionPublic, c.PairingNonce,
		c.IssuedAtMS, c.ExpiresAtMS,
	))
}

func AuthorizeDevice(
	authorizer ed25519.PrivateKey,
	accountID, authorizerDeviceID, subjectDeviceID string,
	subjectIdentityPublic ed25519.PublicKey,
	subjectEncryptionPublic, pairingNonce []byte,
	now time.Time,
) (DeviceAuthorization, error) {
	if len(authorizer) != ed25519.PrivateKeySize ||
		len(subjectIdentityPublic) != ed25519.PublicKeySize ||
		len(subjectEncryptionPublic) != 32 || len(pairingNonce) < 16 ||
		accountID == "" || authorizerDeviceID == "" || subjectDeviceID == "" {
		return DeviceAuthorization{}, errors.New("invalid device authorization input")
	}
	cert := DeviceAuthorization{
		Version:                 ProtocolVersion,
		AccountID:               accountID,
		AuthorizerDeviceID:      authorizerDeviceID,
		SubjectDeviceID:         subjectDeviceID,
		SubjectIdentityPublic:   base64.RawStdEncoding.EncodeToString(subjectIdentityPublic),
		SubjectEncryptionPublic: base64.RawStdEncoding.EncodeToString(subjectEncryptionPublic),
		PairingNonce:            base64.RawStdEncoding.EncodeToString(pairingNonce),
		IssuedAtMS:              now.UnixMilli(),
		ExpiresAtMS:             now.Add(10 * time.Minute).UnixMilli(),
	}
	cert.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(authorizer, deviceAuthorizationCanonical(cert)))
	return cert, nil
}

func VerifyDeviceAuthorization(
	cert DeviceAuthorization,
	trustedAuthorizer ed25519.PublicKey,
	expectedAccountID string,
	now time.Time,
) error {
	if cert.Version != ProtocolVersion || cert.AccountID != expectedAccountID ||
		cert.AuthorizerDeviceID == "" || cert.SubjectDeviceID == "" {
		return errors.New("invalid device authorization binding")
	}
	issued := time.UnixMilli(cert.IssuedAtMS)
	expires := time.UnixMilli(cert.ExpiresAtMS)
	if issued.After(now.Add(maxClockSkew)) || expires.Before(now) || expires.Sub(issued) > maxHelloLifetime {
		return errors.New("device authorization outside validity window")
	}
	identity, errIdentity := base64.RawStdEncoding.DecodeString(cert.SubjectIdentityPublic)
	exchange, errExchange := base64.RawStdEncoding.DecodeString(cert.SubjectEncryptionPublic)
	nonce, errNonce := base64.RawStdEncoding.DecodeString(cert.PairingNonce)
	signature, errSignature := base64.RawStdEncoding.DecodeString(cert.Signature)
	if errIdentity != nil || errExchange != nil || errNonce != nil || errSignature != nil ||
		len(identity) != ed25519.PublicKeySize || len(exchange) != 32 || len(nonce) < 16 {
		return errors.New("invalid device authorization material")
	}
	if !ed25519.Verify(trustedAuthorizer, deviceAuthorizationCanonical(cert), signature) {
		return errors.New("untrusted device authorization")
	}
	return nil
}

// SafetyFingerprint is display-only verification material. It is derived from
// both device public keys and never replaces certificate verification.
func SafetyFingerprint(identityPublic, encryptionPublic []byte) string {
	h := sha256.New()
	h.Write([]byte("yaver-safety-fingerprint-v1\n"))
	h.Write(identityPublic)
	h.Write(encryptionPublic)
	sum := h.Sum(nil)
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", sum[0], sum[1], sum[2], sum[3], sum[4], sum[5])
}
