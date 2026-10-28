package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrToolLoopDetected is returned when the agent repeats a recent tool call
// with the same arguments. Retrying such a call cannot produce new
// information and burns the budget, so the step fails fast.
var ErrToolLoopDetected = errors.New("agent: repeated tool call detected")

// maxRecentCalls is the number of recent tool calls kept for loop detection.
const maxRecentCalls = 3

// RecentCallHashes carries hashes of the most recent tool calls between
// workflow steps. The workflow persists it in StepResult and passes it back in
// StepInput, which keeps loop detection deterministic across activity retries
// and workflow replays.
type RecentCallHashes []string

// Add hashes the tool call, appends it to the recent window and reports
// whether the call repeats one of the recent calls.
func (r RecentCallHashes) Add(name string, args json.RawMessage) (RecentCallHashes, bool) {
	hash := hashToolCall(name, args)
	for _, prev := range r {
		if prev == hash {
			return r, true
		}
	}

	out := make(RecentCallHashes, 0, len(r)+1)
	out = append(out, r...)
	out = append(out, hash)
	if len(out) > maxRecentCalls {
		out = out[len(out)-maxRecentCalls:]
	}
	return out, false
}

// hashToolCall returns a stable hash of the tool name and normalized arguments.
func hashToolCall(name string, args json.RawMessage) string {
	if len(args) > 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, args); err == nil {
			args = compact.Bytes()
		}
	}

	h := sha256.New()
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(args)
	return hex.EncodeToString(h.Sum(nil))
}

// recordToolCall adds the call to the recent window and returns a
// non-retryable loop error when the call repeats.
func recordToolCall(recent RecentCallHashes, name string, args json.RawMessage) (RecentCallHashes, error) {
	updated, loop := recent.Add(name, args)
	if loop {
		return updated, fmt.Errorf("%w: tool %q with identical arguments", ErrToolLoopDetected, name)
	}
	return updated, nil
}
