package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const datacenterStorageReadinessTimeout = 2 * time.Second

type DatacenterStorageSummary struct {
	SchemaVersion int                   `json:"schemaVersion"`
	ProfileID     string                `json:"profileId"`
	Backend       string                `json:"backend"`
	Readiness     DatacenterReadiness   `json:"readiness"`
	ReasonCode    DatacenterReasonCode  `json:"reasonCode,omitempty"`
	RouteToFix    *DatacenterRouteToFix `json:"routeToFix,omitempty"`
	ReadOnly      bool                  `json:"readOnly"`
	Services      []string              `json:"services"`
	MeasuredAt    time.Time             `json:"measuredAt"`
	ExpiresAt     time.Time             `json:"expiresAt"`
	LocalEvidence string                `json:"-"`
}

// datacenterStorageReadiness adapts the existing shared-storage operations.
// It is deliberately read-only: DC-201 owns the explicit exact-key write,
// read-back and delete probe under .yaver-probe/.
func datacenterStorageReadiness(ctx context.Context, profile SharedStorageProfile) DatacenterStorageSummary {
	now := time.Now().UTC()
	out := DatacenterStorageSummary{
		SchemaVersion: DatacenterSchemaVersion,
		ProfileID:     profile.ID,
		Backend:       datacenterStorageBackend(profile.Type),
		Readiness:     DatacenterReadinessUnknown,
		ReadOnly:      profile.ReadOnly,
		Services:      []string{"files"},
		MeasuredAt:    now,
		ExpiresAt:     now.Add(30 * time.Second),
	}
	if out.Backend == "unknown" {
		out.block(DatacenterReasonStorageUnsupported, "Update storage profile", "unsupported storage backend")
		return out
	}
	if strings.TrimSpace(profile.ID) == "" {
		out.block(DatacenterReasonStorageNotConfigured, "Update storage profile", "profile id required")
		return out
	}
	if _, err := normalizeSharedStorageProfile(profile); err != nil {
		out.block(DatacenterReasonStorageNotConfigured, "Update storage profile", err.Error())
		return out
	}

	probeCtx, cancel := context.WithTimeout(ctx, datacenterStorageReadinessTimeout)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := listSharedStorageEntries(profile, "")
		result <- err
	}()
	select {
	case <-probeCtx.Done():
		out.block(DatacenterReasonStorageTimedOut, "Retry storage check", probeCtx.Err().Error())
	case err := <-result:
		if err != nil {
			out.block(DatacenterReasonStorageUnavailable, "Update storage profile", err.Error())
		} else {
			out.Readiness = DatacenterReadinessReady
		}
	}
	return out
}

func (s *DatacenterStorageSummary) block(code DatacenterReasonCode, label, evidence string) {
	s.Readiness = DatacenterReadinessBlocked
	s.ReasonCode = code
	s.RouteToFix = &DatacenterRouteToFix{
		Label:  label,
		Method: "POST",
		Path:   "/shared-storage/profiles",
	}
	s.LocalEvidence = evidence
}

func datacenterStorageBackend(profileType string) string {
	switch strings.ToLower(strings.TrimSpace(profileType)) {
	case "local":
		return "local"
	case "smb":
		return "smb"
	case "webdav":
		return "webdav"
	case "storagebox":
		return "storagebox"
	case "s3":
		return "s3"
	default:
		return "unknown"
	}
}

func datacenterStorageReadinessAll(ctx context.Context, profiles []SharedStorageProfile) []DatacenterStorageSummary {
	out := make([]DatacenterStorageSummary, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, datacenterStorageReadiness(ctx, profile))
	}
	return out
}

func (s DatacenterStorageSummary) validateSafe() error {
	if s.SchemaVersion != DatacenterSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", s.SchemaVersion)
	}
	if s.ProfileID == "" {
		return fmt.Errorf("profile id required")
	}
	if s.Backend == "unknown" {
		return fmt.Errorf("known backend required")
	}
	return nil
}
