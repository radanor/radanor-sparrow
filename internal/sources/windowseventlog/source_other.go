//go:build !windows

package windowseventlog

import (
	"context"
	"github.com/radanor/radanor-shield/internal/monitoring"
)

const SourceID = "native.windows_defender_events"

type Source struct{}

func New() *Source { return &Source{} }
func (*Source) Descriptor() monitoring.Descriptor {
	return monitoring.Descriptor{ContractVersion: monitoring.SourceContractV1, ID: SourceID, Platforms: []string{"windows-11"}, Capabilities: []string{"filtered Windows Defender operational events"}, RequiredAccess: monitoring.PrivilegeUser, EventTypes: []string{"defender.detection", "defender.action", "defender.protection_changed", "defender.configuration_changed"}, SchemaVersion: monitoring.EventSchemaV1, Recovery: monitoring.RecoveryCheckpoint, StartupBehavior: "Windows-only strict pull subscription", ShutdownBehavior: "context cancellation closes native handles", HealthReporting: "explicit after subscription", Limitations: []string{"unsupported on this platform"}}
}
func (*Source) Run(context.Context, []byte, monitoring.EmitFunc, monitoring.Reporter) error {
	return monitoring.ErrUnsupported
}
