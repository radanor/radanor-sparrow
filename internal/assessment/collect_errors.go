package assessment

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// classifyCollectError maps a collector failure onto an honest collection
// status. A timeout becomes "failed" (the deadline elapsed), a context
// cancellation propagates, and a genuine operating-system refusal becomes
// permission_denied. Callers never turn these into a FAIL verdict.
func classifyCollectError(ctx context.Context, err error) CollectionStatus {
	if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
		return Failed
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Failed
	}
	if errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
		if errors.Is(err, os.ErrPermission) {
			return PermissionDenied
		}
		return Unsupported
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A tool that exited non-zero without a refusal is an incomplete read,
		// not a security verdict.
		return Incomplete
	}
	return Failed
}

// boundCollectError trims collector error text so unbounded or hostile output
// cannot inflate stored records.
func boundCollectError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	return boundText(msg)
}
