// Package assessment provides the platform-neutral security posture assessment
// framework (CE-5). It gathers factual observations, evaluates them with
// versioned rules, and produces evidence-backed findings. It never claims a
// computer is secure because no vulnerability or suspicious event was seen, and
// it performs no system modification, scanning, or remediation.
package assessment

import (
	"time"
)

const (
	// SchemaVersion stamps every durable assessment record produced by this build.
	SchemaVersion = "shield.assessment/v1"
	// MaxObservationBytes bounds a single serialized observation's evidence.
	MaxObservationBytes = 8 * 1024
	// MaxEvidenceValues bounds the number of typed values an observation carries.
	MaxEvidenceValues = 64
	// MaxTextValue bounds any single textual evidence value or explanation.
	MaxTextValue = 512
	// DefaultCheckFreshness is the freshness budget applied to a check
	// definition that does not declare one of its own. It exists so an omitted
	// MaxAge is a *policy*, not the absence of one: without a default a
	// definition with no budget would be treated as forever-fresh, and a verdict
	// could be reconfirmed indefinitely from stale evidence.
	DefaultCheckFreshness = 15 * time.Minute
)

// CollectionStatus reports how a collector's attempt concluded. It is separate
// from the security verdict so that "could not collect" is never confused with
// "secure".
type CollectionStatus string

const (
	// Collected means the collector produced usable observations.
	Collected CollectionStatus = "collected"
	// PermissionDenied means the operating system refused the required access.
	PermissionDenied CollectionStatus = "permission_denied"
	// Unsupported means the platform or interface genuinely does not apply.
	Unsupported CollectionStatus = "unsupported"
	// Incomplete means some but not all facts could be gathered.
	Incomplete CollectionStatus = "incomplete"
	// Failed means the collector errored for an unexpected reason.
	Failed CollectionStatus = "failed"
)

// Result is the outcome of evaluating one check against observations.
type Result string

const (
	Pass         Result = "pass"
	Fail         Result = "fail"
	Unknown      Result = "unknown"
	NotSupported Result = "not_supported"
	// Stale means the freshest supporting observation exceeded its freshness
	// budget, so the previous verdict cannot be reconfirmed.
	Stale Result = "stale"
)

// Severity ranks a confirmed weakness. UNKNOWN and NOT_SUPPORTED carry no
// severity because no weakness was confirmed.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Lifecycle tracks one finding across scans.
type Lifecycle string

const (
	LifecycleNew        Lifecycle = "new"
	LifecycleContinuing Lifecycle = "continuing"
	LifecycleResolved   Lifecycle = "resolved"
	LifecycleReopened   Lifecycle = "reopened"
	// LifecycleSuperseded marks a finding raised by a rule version that is no
	// longer the one in use. It is deliberately distinct from resolved: the
	// weakness was never verified as remediated, the rule that reported it was
	// replaced, so the audit trail must not read as a fix.
	LifecycleSuperseded Lifecycle = "superseded"
)

// IsOpen reports whether a finding still tracks a live weakness. Open findings
// are the ones counted in the summary and shown as actionable; superseded and
// resolved findings are retained for history.
func (l Lifecycle) IsOpen() bool {
	return l == LifecycleNew || l == LifecycleContinuing || l == LifecycleReopened
}

// ValueKind bounds the typed evidence a collector may report.
type ValueKind string

const (
	ValueBool   ValueKind = "bool"
	ValueString ValueKind = "string"
	ValueInt    ValueKind = "int"
)

// Value is one typed evidence entry. Text values are bounded and are facts,
// never verdicts.
type Value struct {
	Key  string    `json:"key"`
	Kind ValueKind `json:"kind"`
	Bool *bool     `json:"bool,omitempty"`
	Int  *int64    `json:"int,omitempty"`
	Text string    `json:"text,omitempty"`
}

// Observation is a factual, timestamped reading produced by a collector. It
// carries no security verdict.
type Observation struct {
	ID          string           `json:"id"`
	Collector   string           `json:"collector"`
	Platform    string           `json:"platform"`
	CollectedAt time.Time        `json:"collected_at"`
	Status      CollectionStatus `json:"status"`
	Source      string           `json:"source"`
	Values      []Value          `json:"values"`
	Error       string           `json:"error,omitempty"`
	DocURL      string           `json:"doc_url,omitempty"`
}

// CheckDefinition is a versioned rule. The version participates in the finding
// fingerprint so a materially changed rule cannot silently reopen or resolve a
// finding from an older rule.
type CheckDefinition struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// CollectorID binds this check to the collector that supplies its evidence.
	CollectorID  string        `json:"collector_id"`
	Category     string        `json:"category"`
	Title        string        `json:"title"`
	Platforms    []string      `json:"platforms"`
	Severity     Severity      `json:"severity"`
	Explanation  string        `json:"explanation"`
	Action       string        `json:"action"`
	VerifyMethod string        `json:"verify_method"`
	Limitations  []string      `json:"limitations,omitempty"`
	MaxAge       time.Duration `json:"-"`
	// EvaluateFn applies the versioned rule to one observation. It is a field,
	// not a method, so CheckDefinition stays a plain comparable value.
	EvaluateFn func(Observation) Eval `json:"-"`
}

// Eval is the verdict produced by a check's evaluation function.
type Eval struct {
	Result Result `json:"result"`
	Detail string `json:"detail,omitempty"`
}

// CheckResult is the evidence-backed outcome for one check in one run.
type CheckResult struct {
	CheckID      string      `json:"check_id"`
	CheckVersion string      `json:"check_version"`
	Category     string      `json:"category"`
	Title        string      `json:"title"`
	Platforms    []string    `json:"platforms"`
	Result       Result      `json:"result"`
	Severity     Severity    `json:"severity,omitempty"`
	Detail       string      `json:"detail,omitempty"`
	Explanation  string      `json:"explanation"`
	Action       string      `json:"action"`
	VerifyMethod string      `json:"verify_method"`
	Limitations  []string    `json:"limitations,omitempty"`
	Observation  Observation `json:"observation"`
	CollectedAt  time.Time   `json:"collected_at"`
	EvaluatedAt  time.Time   `json:"evaluated_at"`
	FreshnessAge string      `json:"freshness_age"`
	Fresh        bool        `json:"fresh"`
}

// Findings are generated only for confirmed weaknesses (FAIL), never for
// UNKNOWN, NOT_SUPPORTED, or STALE outcomes.
func GeneratesFinding(r Result) bool { return r == Fail }
