package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// signSigV4 applies AWS Signature Version 4 to the request in-place. Works
// against real AWS S3, Cloudflare R2, Backblaze B2, and any other SigV4-
// compatible object store. Region is extracted from the Endpoint host for AWS
// or defaults to "auto" for R2.
//
// Reference: https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
func signSigV4(req *http.Request, access, secret, region, service string, body []byte) {
	if region == "" {
		region = "auto"
	}
	if service == "" {
		service = "s3"
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	payloadHash := sha256hex(body)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if req.URL.Host != "" {
		req.Header.Set("Host", req.URL.Host)
	}

	// Canonical request.
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalizeQuery(req.URL.Query())
	signedHeaders, canonicalHeaders := buildCanonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign.
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256hex([]byte(canonicalRequest)),
	}, "\n")

	// Signing key.
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authz := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		access, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authz)
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func canonicalizeQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := values[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, awsURIEscape(k)+"="+awsURIEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsURIEscape matches AWS's stricter URI encoding (spaces → %20, not +).
func awsURIEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func buildCanonicalHeaders(req *http.Request) (signed, canonical string) {
	// Always sign Host + x-amz-* headers.
	keys := []string{"host"}
	for k := range req.Header {
		lower := strings.ToLower(k)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" {
			keys = append(keys, lower)
		}
	}
	sort.Strings(keys)
	// Dedup.
	dedup := keys[:0]
	last := ""
	for _, k := range keys {
		if k != last {
			dedup = append(dedup, k)
			last = k
		}
	}

	var sb strings.Builder
	for _, k := range dedup {
		var v string
		if k == "host" {
			v = req.URL.Host
		} else {
			v = strings.Join(req.Header.Values(canonicalHeaderName(k)), ",")
		}
		v = strings.TrimSpace(v)
		// Collapse inner whitespace.
		for strings.Contains(v, "  ") {
			v = strings.ReplaceAll(v, "  ", " ")
		}
		sb.WriteString(k)
		sb.WriteString(":")
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	return strings.Join(dedup, ";"), sb.String()
}

func canonicalHeaderName(lower string) string {
	parts := strings.Split(lower, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "-")
}

// regionFromS3URL extracts a region from an AWS S3 endpoint. Returns empty if
// not recognized (R2 uses "auto"; MinIO doesn't care).
func regionFromS3URL(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	// AWS global: s3.us-east-1.amazonaws.com / s3-us-west-2.amazonaws.com
	host := u.Host
	if strings.HasSuffix(host, ".amazonaws.com") {
		parts := strings.Split(host, ".")
		for _, p := range parts {
			if strings.HasPrefix(p, "s3-") {
				return p[3:]
			}
			if strings.HasPrefix(p, "us-") || strings.HasPrefix(p, "eu-") ||
				strings.HasPrefix(p, "ap-") || strings.HasPrefix(p, "ca-") ||
				strings.HasPrefix(p, "sa-") || strings.HasPrefix(p, "af-") ||
				strings.HasPrefix(p, "me-") {
				return p
			}
		}
		return "us-east-1"
	}
	return ""
}
