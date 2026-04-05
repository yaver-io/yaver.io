package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	// HermesMagicOffset is where the magic number lives in the HBC file header.
	// Offset 4, NOT offset 0 — the first 4 bytes are a different field.
	HermesMagicOffset = 4
	// HermesBCVersionOffset is where the bytecode version lives.
	HermesBCVersionOffset = 8
	// HermesMagic is the Hermes bytecode magic number at offset 4.
	HermesMagic uint32 = 0x1F1903C1
	// MinBundleSize — anything smaller is definitely broken.
	MinBundleSize int64 = 1024
	// MaxBundleSize — 100MB sanity cap.
	MaxBundleSize int64 = 100 * 1024 * 1024
)

// BundleMetadata is sent to mobile before/with the bundle bytes.
// Both Go and Swift must use the same JSON structure.
type BundleMetadata struct {
	Version         int    `json:"version"`         // protocol version, always 1
	Size            int64  `json:"size"`            // exact byte count of HBC file
	MD5             string `json:"md5"`             // hex-encoded MD5 of entire HBC file
	HermesBCVersion int    `json:"hermesBCVersion"` // bytecode version (e.g. 96)
	ModuleName      string `json:"moduleName"`      // AppRegistry component name
	Format          string `json:"format"`          // "hbc" or "js"
}

// ValidateHBC checks a built HBC bundle file.
// Returns metadata if valid, error if not.
// Call this BEFORE sending to phone. If error, DO NOT send.
func ValidateHBC(filePath string, expectedBCVersion int) (*BundleMetadata, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot open bundle: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot stat bundle: %w", err)
	}
	if stat.Size() < MinBundleSize {
		return nil, fmt.Errorf(
			"bundle too small: %d bytes (minimum %d). Build likely failed silently",
			stat.Size(), MinBundleSize,
		)
	}
	if stat.Size() > MaxBundleSize {
		return nil, fmt.Errorf(
			"bundle too large: %d bytes (maximum %d). Something is wrong",
			stat.Size(), MaxBundleSize,
		)
	}

	// Read first 12 bytes for magic + BC version
	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return nil, fmt.Errorf("cannot read header: %w", err)
	}

	// Hermes magic at offset 4
	magic := uint32(header[4]) | uint32(header[5])<<8 | uint32(header[6])<<16 | uint32(header[7])<<24
	if magic != HermesMagic {
		return nil, fmt.Errorf(
			"NOT a Hermes bytecode bundle. Magic at offset 4: 0x%08X, expected: 0x%08X. "+
				"This is likely a raw JS bundle from Metro — it must be compiled with hermesc first",
			magic, HermesMagic,
		)
	}

	// BC version at offset 8
	bcVersion := uint32(header[8]) | uint32(header[9])<<8 | uint32(header[10])<<16 | uint32(header[11])<<24
	if expectedBCVersion > 0 && int(bcVersion) != expectedBCVersion {
		return nil, fmt.Errorf(
			"bytecode version mismatch: bundle has BC%d, Yaver app expects BC%d. "+
				"Use the embedded hermesc, not the third-party app's",
			bcVersion, expectedBCVersion,
		)
	}

	// MD5 of entire file
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("cannot seek: %w", err)
	}
	hasher := md5.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return nil, fmt.Errorf("cannot compute MD5: %w", err)
	}

	return &BundleMetadata{
		Version:         1,
		Size:            stat.Size(),
		MD5:             hex.EncodeToString(hasher.Sum(nil)),
		HermesBCVersion: int(bcVersion),
		Format:          "hbc",
	}, nil
}

// BundleMetadataJSON returns the metadata as a JSON string for the X-Yaver-Bundle-Metadata header.
func (m *BundleMetadata) JSON() string {
	b, _ := json.Marshal(m)
	return string(b)
}
