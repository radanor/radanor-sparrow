package collectors

import "github.com/radanor/radanor-shield/internal/assessment"

// maxEndpointSamples bounds how many individual sockets are echoed into network
// evidence so a host with thousands of sockets cannot bloat a record. The
// counts themselves remain exact on every platform.
const maxEndpointSamples = 20

// Typed accessors for reading a collector's factual values inside a check's
// evaluator. Every accessor is total: a missing or differently-typed key yields
// "not present" so an evaluator can report UNKNOWN honestly instead of
// defaulting to a security verdict.

// lookupValue returns the evidence value stored under key, if any.
func lookupValue(obs assessment.Observation, key string) (assessment.Value, bool) {
	for _, v := range obs.Values {
		if v.Key == key {
			return v, true
		}
	}
	return assessment.Value{}, false
}

// boolValue returns a boolean evidence value.
func boolValue(obs assessment.Observation, key string) (bool, bool) {
	v, ok := lookupValue(obs, key)
	if !ok || v.Kind != assessment.ValueBool || v.Bool == nil {
		return false, false
	}
	return *v.Bool, true
}

// intValue returns an integer evidence value.
func intValue(obs assessment.Observation, key string) (int64, bool) {
	v, ok := lookupValue(obs, key)
	if !ok || v.Kind != assessment.ValueInt || v.Int == nil {
		return 0, false
	}
	return *v.Int, true
}

// stringValue returns a textual evidence value.
func stringValue(obs assessment.Observation, key string) (string, bool) {
	v, ok := lookupValue(obs, key)
	if !ok || v.Kind != assessment.ValueString {
		return "", false
	}
	return v.Text, true
}

// unknownEval is the shared "no reliable evidence, no verdict" outcome used
// whenever an evaluator cannot ground a PASS or FAIL in collected facts.
func unknownEval(detail string) assessment.Eval {
	return assessment.Eval{Result: assessment.Unknown, Detail: detail}
}
