package main

import "time"

const (
	DatacenterSchemaVersion   = 1
	DatacenterProtocolVersion = 1
)

type DatacenterReadiness string

const (
	DatacenterReadinessReady   DatacenterReadiness = "ready"
	DatacenterReadinessBlocked DatacenterReadiness = "blocked"
	DatacenterReadinessUnknown DatacenterReadiness = "unknown"
	DatacenterReadinessProbing DatacenterReadiness = "probing"
)

type DatacenterReasonCode string

const (
	DatacenterReasonNone                 DatacenterReasonCode = ""
	DatacenterReasonStorageNotConfigured DatacenterReasonCode = "datacenter.storage.not_configured"
	DatacenterReasonStorageUnsupported   DatacenterReasonCode = "datacenter.storage.unsupported"
	DatacenterReasonStorageTimedOut      DatacenterReasonCode = "datacenter.storage.timed_out"
	DatacenterReasonStorageUnavailable   DatacenterReasonCode = "datacenter.storage.unavailable"
)

type DatacenterCompatibility string

const (
	DatacenterCompatibilityCompatible  DatacenterCompatibility = "compatible"
	DatacenterCompatibilityLegacy      DatacenterCompatibility = "legacy"
	DatacenterCompatibilityUnsupported DatacenterCompatibility = "unsupported"
	DatacenterCompatibilityUnknown     DatacenterCompatibility = "unknown"
)

type DatacenterRouteToFix struct {
	Label  string `json:"label"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Stream string `json:"stream,omitempty"`
}

type DatacenterResourceVector struct {
	CPUMillis      int64 `json:"cpuMillis,omitempty"`
	MemoryBytes    int64 `json:"memoryBytes,omitempty"`
	ScratchBytes   int64 `json:"scratchBytes,omitempty"`
	StorageIOUnits int64 `json:"storageIOUnits,omitempty"`
	ExclusiveSlots int64 `json:"exclusiveSlots,omitempty"`
}

type DatacenterCapabilityProbe struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Capability    string                   `json:"capability"`
	Readiness     DatacenterReadiness      `json:"readiness"`
	ReasonCode    DatacenterReasonCode     `json:"reasonCode,omitempty"`
	MeasuredAt    time.Time                `json:"measuredAt"`
	ExpiresAt     time.Time                `json:"expiresAt"`
	Summary       string                   `json:"summary,omitempty"`
	RouteToFix    *DatacenterRouteToFix    `json:"routeToFix,omitempty"`
	Resources     DatacenterResourceVector `json:"resources,omitempty"`
	LocalEvidence string                   `json:"-"`
}

// DatacenterStatus is the safe, roaming status envelope. Detailed probe
// evidence stays on the node and is intentionally absent from this contract.
type DatacenterStatus struct {
	SchemaVersion   int                              `json:"schemaVersion"`
	ProtocolVersion int                              `json:"protocolVersion"`
	GeneratedAt     time.Time                        `json:"generatedAt"`
	ExpiresAt       time.Time                        `json:"expiresAt"`
	Readiness       DatacenterReadiness              `json:"readiness"`
	ReasonCode      DatacenterReasonCode             `json:"reasonCode,omitempty"`
	Summary         string                           `json:"summary,omitempty"`
	RouteToFix      *DatacenterRouteToFix            `json:"routeToFix,omitempty"`
	Nodes           []DatacenterNodeCapabilityDigest `json:"nodes,omitempty"`
}

type DatacenterNodeCapabilityDigest struct {
	SchemaVersion   int                         `json:"schemaVersion"`
	ProtocolVersion int                         `json:"protocolVersion"`
	GeneratedAt     time.Time                   `json:"generatedAt"`
	ExpiresAt       time.Time                   `json:"expiresAt"`
	Digest          string                      `json:"digest,omitempty"`
	DeviceID        string                      `json:"deviceId"`
	OS              string                      `json:"os"`
	Arch            string                      `json:"arch"`
	Compatibility   DatacenterCompatibility     `json:"compatibility"`
	Capabilities    []DatacenterCapabilityProbe `json:"capabilities,omitempty"`
	Storage         []DatacenterStorageSummary  `json:"storage,omitempty"`
}

type DatacenterLease struct {
	SchemaVersion int                      `json:"schemaVersion"`
	LeaseID       string                   `json:"leaseId"`
	ResourceKey   string                   `json:"resourceKey"`
	HolderJobID   string                   `json:"holderJobId"`
	AttemptID     string                   `json:"attemptId"`
	Term          uint64                   `json:"term"`
	Units         DatacenterResourceVector `json:"units"`
	ExpiresAt     time.Time                `json:"expiresAt"`
	RenewedAt     time.Time                `json:"renewedAt"`
	Authority     string                   `json:"authority"`
}

type DatacenterSourceRef struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Revision      string `json:"revision,omitempty"`
	PatchHash     string `json:"patchHash,omitempty"`
	ContentHash   string `json:"contentHash,omitempty"`
}

type DatacenterArtifactRef struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ContentHash     string `json:"contentHash"`
	Type            string `json:"type"`
	SizeBytes       int64  `json:"sizeBytes"`
	ProducerAttempt string `json:"producerAttempt"`
	SourceRevision  string `json:"sourceRevision,omitempty"`
	StorageProfile  string `json:"storageProfile,omitempty"`
}

type DatacenterWorkloadSpec struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	ProtocolVersion int                      `json:"protocolVersion"`
	WorkloadID      string                   `json:"workloadId"`
	IdempotencyKey  string                   `json:"idempotencyKey"`
	ProjectID       string                   `json:"projectId"`
	Kind            string                   `json:"kind"`
	Target          string                   `json:"target,omitempty"`
	Source          DatacenterSourceRef      `json:"source"`
	Resources       DatacenterResourceVector `json:"resources,omitempty"`
	ApprovalClass   string                   `json:"approvalClass"`
	Idempotency     string                   `json:"idempotency"`
}

type DatacenterJob struct {
	SchemaVersion int       `json:"schemaVersion"`
	JobID         string    `json:"jobId"`
	WorkloadID    string    `json:"workloadId"`
	State         string    `json:"state"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type DatacenterJobAttempt struct {
	SchemaVersion int                     `json:"schemaVersion"`
	AttemptID     string                  `json:"attemptId"`
	JobID         string                  `json:"jobId"`
	DeviceID      string                  `json:"deviceId"`
	State         string                  `json:"state"`
	Input         DatacenterSourceRef     `json:"input"`
	Lease         *DatacenterLease        `json:"lease,omitempty"`
	Failure       DatacenterReasonCode    `json:"failureCode,omitempty"`
	Artifacts     []DatacenterArtifactRef `json:"artifacts,omitempty"`
	StartedAt     time.Time               `json:"startedAt,omitempty"`
	EndedAt       time.Time               `json:"endedAt,omitempty"`
}
