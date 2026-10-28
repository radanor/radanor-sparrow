package filewatch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/radanor/radanor-shield/internal/monitoring"
)

const (
	SourceID = "native.file_integrity"
	MaxRoots = 8
)

type Root struct{ Label, Path string }

type Source struct {
	roots []Root
	// instance discriminates one run of the process from the next. Native file
	// notification identity is a counter that restarts at 1 in every process, so
	// without this discriminator a second session emits the identifiers the first
	// session already stored, and the store's (source, event_id) uniqueness would
	// silently discard the beginning of every session after the first.
	instance string
}

func New(roots []Root) (*Source, error) {
	if len(roots) == 0 || len(roots) > MaxRoots {
		return nil, errors.New("file monitoring requires 1-8 roots")
	}
	clean := make([]Root, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		if root.Label == "" || len(root.Label) > 32 || strings.ContainsAny(root.Label, "/\\\r\n") {
			return nil, errors.New("invalid root label")
		}
		path, err := filepath.Abs(root.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", root.Label, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("root %s must be a real directory", root.Label)
		}
		path = filepath.Clean(path)
		if seen[path] {
			return nil, errors.New("duplicate monitoring root")
		}
		seen[path] = true
		clean = append(clean, Root{Label: root.Label, Path: path})
	}
	instance, err := newInstanceID()
	if err != nil {
		// Fail closed. Running with a predictable discriminator would mean every
		// event of this session collides with an event of another, which is the
		// one thing the identity exists to prevent.
		return nil, err
	}
	return &Source{roots: clean, instance: instance}, nil
}

// instanceIDBytes is the entropy behind one run's discriminator: 128 bits, whose
// birthday bound is small enough that two runs colliding is not a practical
// event rather than merely an unlikely one.
const instanceIDBytes = 16

// randomRead is a seam, so the fail-closed behaviour below is testable without
// breaking the machine's random source.
var randomRead = rand.Read

// newInstanceID returns a value that is distinct for every run of the process.
//
// It uses the operating system's CSPRNG and fails closed: a predictable
// discriminator would make this run's event identifiers collide with another
// run's, and the store would discard the beginning of the session as duplicates.
// The previous fallback derived it from the clock, which is guessable, is not
// distinct between two processes started in the same instant, and can even go
// backwards across a clock adjustment.
func newInstanceID() (string, error) {
	raw := make([]byte, instanceIDBytes)
	if _, err := randomRead(raw); err != nil {
		return "", fmt.Errorf("file monitoring needs a working system random source: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// eventID renders the identity of one observed native file event. The value is
// stable within a run, so a genuine redelivery of the same observation is still
// recognised as a duplicate, and unique across runs, so a later session never
// looks like a replay of an earlier one.
func (s *Source) eventID(prefix string, seq uint64) string {
	return prefix + s.instance + ":" + strconv.FormatUint(seq, 10)
}

func Default() (*Source, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var candidates []Root
	if runtime.GOOS == "windows" {
		app := os.Getenv("APPDATA")
		if app != "" {
			candidates = append(candidates, Root{Label: "startup", Path: filepath.Join(app, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")})
		}
	} else {
		candidates = append(candidates, Root{Label: "autostart", Path: filepath.Join(home, ".config", "autostart")})
	}
	candidates = append(candidates, Root{Label: "ssh", Path: filepath.Join(home, ".ssh")})
	roots := candidates[:0]
	for _, root := range candidates {
		if info, statErr := os.Lstat(root.Path); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil, monitoring.ErrUnsupported
	}
	return New(roots)
}

func (s *Source) Descriptor() monitoring.Descriptor {
	return monitoring.Descriptor{ContractVersion: monitoring.SourceContractV1, ID: SourceID, Platforms: []string{"linux", "windows"}, Capabilities: []string{"nonrecursive file create, modify, delete, and rename observations"}, RequiredAccess: monitoring.PrivilegeUser, EventTypes: []string{"file.created", "file.modified", "file.deleted", "file.renamed_from", "file.renamed_to"}, SchemaVersion: monitoring.EventSchemaV1, Recovery: monitoring.RecoveryNone, StartupBehavior: "opens a bounded allowlist of existing user directories", ShutdownBehavior: "cancellation closes all native handles", HealthReporting: "confirms only after every configured native watch starts", Limitations: []string{"nonrecursive", "no durable replay", "native queues may overflow", "event identity is scoped to one run of the process"}}
}
