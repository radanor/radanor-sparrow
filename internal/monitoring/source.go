package monitoring

import (
	"context"
	"time"
)

const SourceContractV1 = "shield.source/v1"

type Privilege string

const (
	PrivilegeUser        Privilege = "user"
	PrivilegeUnavailable Privilege = "unavailable_without_elevation"
)

type RecoveryCapability string

const (
	RecoveryNone       RecoveryCapability = "none"
	RecoveryCheckpoint RecoveryCapability = "checkpoint"
)

type Descriptor struct {
	ContractVersion  string
	ID               string
	Platforms        []string
	Capabilities     []string
	RequiredAccess   Privilege
	EventTypes       []string
	SchemaVersion    string
	Recovery         RecoveryCapability
	StartupBehavior  string
	ShutdownBehavior string
	HealthReporting  string
	Limitations      []string
}

func (d Descriptor) Validate() error {
	if d.ContractVersion != SourceContractV1 || d.ID == "" || d.SchemaVersion != EventSchemaV1 || len(d.Platforms) == 0 || len(d.Capabilities) == 0 || len(d.EventTypes) == 0 || d.StartupBehavior == "" || d.ShutdownBehavior == "" || d.HealthReporting == "" {
		return ErrInvalidSource
	}
	return nil
}

type EmitFunc func(context.Context, Event) error
type Reporter interface {
	ConfirmHealthy()
	ReportGap(start, end time.Time, reason string)
}

// Source owns startup and shutdown. Run must stop after ctx cancellation.
type Source interface {
	Descriptor() Descriptor
	Run(context.Context, []byte, EmitFunc, Reporter) error
}

var (
	ErrInvalidSource     = sourceError("invalid source descriptor")
	ErrPermissionDenied  = sourceError("permission denied")
	ErrUnsupported       = sourceError("unsupported")
	ErrInvalidCheckpoint = sourceError("invalid checkpoint")
	ErrQueueFull         = sourceError("queue full")
)

type sourceError string

func (e sourceError) Error() string { return string(e) }
