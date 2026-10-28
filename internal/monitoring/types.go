// Package monitoring provides the platform-neutral, bounded monitoring engine.
// It intentionally contains no operating-system event subscriptions.
package monitoring

import (
	"errors"
	"strings"
	"time"
)

const (
	EventSchemaV1   = "shield.event/v1"
	MaxMetadataKeys = 32
	MaxKeyBytes     = 64
	MaxValueBytes   = 1024
	MaxEventBytes   = 32 * 1024
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type EvidenceClass string

const (
	EvidenceTelemetry EvidenceClass = "telemetry"
	EvidenceAudit     EvidenceClass = "audit_record"
	EvidenceDerived   EvidenceClass = "derived_observation"
)

// Event is an observation, not an alert or malware verdict.
type Event struct {
	ID             string            `json:"id"`
	Source         string            `json:"source"`
	Type           string            `json:"type"`
	Platform       string            `json:"platform"`
	OccurredAt     time.Time         `json:"occurred_at"`
	ObservedAt     time.Time         `json:"observed_at"`
	Severity       Severity          `json:"severity,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Evidence       EvidenceClass     `json:"evidence"`
	SchemaVersion  string            `json:"schema_version"`
	SourceSequence string            `json:"source_sequence,omitempty"`
	Checkpoint     []byte            `json:"checkpoint,omitempty"`
}

func (e Event) Validate(now time.Time) error {
	if e.ID == "" || len(e.ID) > 256 || e.Source == "" || len(e.Source) > 128 || e.Type == "" || len(e.Type) > 128 {
		return errors.New("event identity is missing or too long")
	}
	if e.Platform == "" || len(e.Platform) > 64 || e.OccurredAt.IsZero() || e.ObservedAt.IsZero() {
		return errors.New("event platform and timestamps are required")
	}
	if e.ObservedAt.After(now.Add(5 * time.Minute)) {
		return errors.New("observation timestamp is implausibly in the future")
	}
	if e.SchemaVersion != EventSchemaV1 {
		return errors.New("unsupported event schema")
	}
	if e.Evidence != EvidenceTelemetry && e.Evidence != EvidenceAudit && e.Evidence != EvidenceDerived {
		return errors.New("invalid evidence classification")
	}
	if e.Severity != "" && e.Severity != SeverityInfo && e.Severity != SeverityLow && e.Severity != SeverityMedium && e.Severity != SeverityHigh && e.Severity != SeverityCritical {
		return errors.New("invalid severity")
	}
	if len(e.Metadata) > MaxMetadataKeys {
		return errors.New("too many metadata fields")
	}
	size := len(e.ID) + len(e.Source) + len(e.Type) + len(e.Platform) + len(e.SourceSequence) + len(e.Checkpoint)
	for key, value := range e.Metadata {
		if key == "" || len(key) > MaxKeyBytes || len(value) > MaxValueBytes || strings.ContainsAny(key, "\r\n") {
			return errors.New("invalid metadata")
		}
		size += len(key) + len(value)
	}
	if size > MaxEventBytes {
		return errors.New("event exceeds size limit")
	}
	return nil
}
