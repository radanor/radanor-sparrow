//go:build windows

package collectors

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// windowsPowerShellJSON runs a fixed, self-contained PowerShell command and
// decodes its ConvertTo-Json output.
//
// Why PowerShell at all: several Windows facts (Defender protection state,
// BitLocker volume status, service and socket inventories) have no stable
// registry representation and are only exposed through WMI/CIM COM providers.
// Driving those providers directly would mean hand-writing COM interop, which
// is a large, untestable surface. Instead the collectors ask PowerShell for
// structured objects and pipe them through ConvertTo-Json.
//
// This is deliberately NOT localized-text parsing: the property names of the
// underlying CIM objects are English identifiers that do not change with the
// display language, and ConvertTo-Json emits a machine-readable document. Any
// failure to produce a JSON document is reported as an inconclusive collection
// rather than being interpreted.
//
// The script text is fixed at compile time in each collector; no caller- or
// user-supplied value ever reaches it, and each call is bounded by the shared
// command timeout and output cap.
func windowsPowerShellJSON(ctx context.Context, script string, target any) error {
	res, err := windowsPowerShellOutput(ctx, maxCommandOutput, script)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(res.Output), target); err != nil {
		return errors.New("structured output could not be decoded")
	}
	return nil
}

// windowsPowerShellOutput runs a fixed PowerShell command and returns its
// trimmed, non-empty output together with whether the capture was clipped, for a
// caller that does its own decoding. It exists so that decoding which needs to
// accept more than one document shape (a bare object as well as an array) can
// live in a platform-neutral model and be driven by fixtures, rather than being
// trapped behind the windows build tag.
//
// The capture limit is a parameter because a full service or socket inventory is
// a legitimately large listing while a Defender status document is small; a
// caller that reuses this entry point for an inventory passes the inventory
// bound explicitly rather than inheriting the smaller one.
func windowsPowerShellOutput(ctx context.Context, limit int, script string) (commandResult, error) {
	res, err := runCommandWithCap(ctx, defaultCommandTimeout, limit, "powershell", powershellArgs(script)...)
	if err != nil {
		return res, err
	}
	trimmed := strings.TrimSpace(res.Output)
	if trimmed == "" {
		// An empty document is not a valid answer for these queries.
		return res, errors.New("no structured output was returned")
	}
	res.Output = trimmed
	return res, nil
}
