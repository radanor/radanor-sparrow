//go:build linux

package collectors

import (
	"context"
	"testing"
)

// Collector benchmarks measure genuine execution time on the host running the
// suite. They are reporting tools for the CE-5 performance record, not pass/fail
// gates; run them with `go test -bench . -benchtime 1x ./internal/collectors`.

func BenchmarkCollectors(b *testing.B) {
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"platform", func(ctx context.Context) error { _, err := PlatformCollector{}.Collect(ctx); return err }},
		{"release_support", func(ctx context.Context) error { _, err := SupportCollector{}.Collect(ctx); return err }},
		{"apparmor", func(ctx context.Context) error { _, err := AppArmorCollector{}.Collect(ctx); return err }},
		{"firewall_posture", func(ctx context.Context) error { _, err := FirewallPostureCollector{}.Collect(ctx); return err }},
		{"automatic_security_updates", func(ctx context.Context) error { _, err := AutomaticUpdatesCollector{}.Collect(ctx); return err }},
		{"pending_security_updates", func(ctx context.Context) error { _, err := PendingSecurityUpdatesCollector{}.Collect(ctx); return err }},
		{"disk_encryption", func(ctx context.Context) error { _, err := DiskEncryptionCollector{}.Collect(ctx); return err }},
		{"ssh_server_config", func(ctx context.Context) error { _, err := SSHServerCollector{}.Collect(ctx); return err }},
		{"services", func(ctx context.Context) error { _, err := ServicesCollector{}.Collect(ctx); return err }},
		{"network_endpoints", func(ctx context.Context) error { _, err := NetworkCollector{}.Collect(ctx); return err }},
		{"package_inventory", func(ctx context.Context) error { _, err := PackageInventoryCollector{}.Collect(ctx); return err }},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			// Warm once so the measurement is not dominated by first-run setup.
			_ = tc.run(ctx)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := tc.run(ctx); err != nil {
					b.Fatalf("collector failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkSSHConfigParse isolates the parser's own cost from process spawning,
// which matters because the config file is read and parsed on every scan.
func BenchmarkSSHConfigParse(b *testing.B) {
	text := "PermitRootLogin prohibit-password\nPasswordAuthentication no\n" +
		"# a comment\nMatch Address 10.0.0.0/8\n    PasswordAuthentication yes\n"
	for i := 0; i < b.N; i++ {
		_, _, _ = parseSSHDConfig(text, trackedSSHKeys)
	}
}
