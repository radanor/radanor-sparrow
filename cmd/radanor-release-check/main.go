// Command radanor-release-check is the licensing validation for Radanor Sparrow
// Community Edition.
//
// It runs in one of two modes:
//
//	--mode release      (default) strict; every unresolved placeholder blocks a
//	                    public release, and the command exits non-zero.
//	--mode development  allows the documented placeholders (Licensor identity,
//	                    first-release Change Date, pending decisions) but still
//	                    fails on structural problems: altered license text, a
//	                    missing or invalid structure, a missing Additional Use
//	                    Grant, a missing GPLv3 Change License, an invalid concrete
//	                    Change Date, or inconsistent licensing metadata.
//
// Usage:
//
//	go run ./cmd/radanor-release-check                          # strict metadata check
//	go run ./cmd/radanor-release-check --release-dir dist/linux # also validate the package
//	go run ./cmd/radanor-release-check --mode development       # development validation
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/radanor/radanor-shield/internal/releasecheck"
)

func main() {
	root := flag.String("root", "", "repository root (defaults to discovery from the working directory)")
	releaseDir := flag.String("release-dir", "", "release artifact directory that must carry LICENSE and THIRD_PARTY_NOTICES.md")
	modeName := flag.String("mode", "release", "validation mode: release (strict, fail closed) or development (documented placeholders allowed)")
	flag.Parse()

	mode, ok := parseMode(*modeName)
	if !ok {
		fmt.Fprintf(os.Stderr, "radanor-release-check: unknown mode %q (expected release or development)\n", *modeName)
		os.Exit(2)
	}

	resolvedRoot, err := resolveRoot(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "radanor-release-check: %v\n", err)
		os.Exit(2)
	}

	result := releasecheck.Run(releasecheck.Input{Root: resolvedRoot, ReleaseDir: *releaseDir, Mode: mode})
	blocking := result.Blocking()

	fmt.Printf("radanor-release-check: %s mode.\n", *modeName)
	for _, f := range result.Allowed() {
		fmt.Printf("  allowed    %s: %s\n", f.Code, f.Message)
	}
	if len(blocking) == 0 {
		fmt.Println("radanor-release-check: licensing metadata satisfies this mode.")
	} else {
		fmt.Printf("radanor-release-check: BLOCKED by %d finding(s):\n", len(blocking))
	}
	for _, f := range blocking {
		fmt.Printf("  BLOCKING   %s: %s\n", f.Code, f.Message)
	}

	if len(blocking) > 0 {
		if mode == releasecheck.Development {
			fmt.Fprintln(os.Stderr, "radanor-release-check: development validation failed on structural findings.")
		} else {
			fmt.Fprintln(os.Stderr, "radanor-release-check: refusing to continue. Resolve the findings above before releasing.")
		}
		os.Exit(1)
	}
}

func parseMode(name string) (releasecheck.Mode, bool) {
	switch name {
	case "release":
		return releasecheck.Release, true
	case "development":
		return releasecheck.Development, true
	default:
		return releasecheck.Release, false
	}
}

func resolveRoot(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root (a directory containing go.mod) not found above %s", dir)
		}
		dir = parent
	}
}
