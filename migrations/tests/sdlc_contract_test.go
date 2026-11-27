package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sdlcContract mirrors packages/contracts/state-machine/sdlc_transitions.json.
type sdlcContract struct {
	Version     string           `json:"version"`
	States      []sdlcState      `json:"states"`
	Transitions []sdlcTransition `json:"transitions"`
}

type sdlcState struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type sdlcTransition struct {
	ID        string  `json:"id"`
	From      string  `json:"from"`
	To        string  `json:"to"`
	Initiator string  `json:"initiator"`
	Gate      *string `json:"gate"`
	Condition string  `json:"condition"`
	Error     struct {
		Status     string `json:"status"`
		Audit      bool   `json:"audit"`
		Escalation string `json:"escalation"`
	} `json:"error"`
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	// The test package lives in migrations/tests, so the repository root is
	// two levels up.
	data, err := os.ReadFile(filepath.Join(wd, "..", "..", rel))
	require.NoError(t, err, "read %s", rel)
	return string(data)
}

func loadSDLCContract(t *testing.T) sdlcContract {
	t.Helper()
	var contract sdlcContract
	require.NoError(t, json.Unmarshal(
		[]byte(readRepoFile(t, "packages/contracts/state-machine/sdlc_transitions.json")),
		&contract,
	))
	return contract
}

func extractMarkdownSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	require.GreaterOrEqual(t, start, 0, "section %q not found", heading)
	rest := doc[start+len(heading):]
	if next := regexp.MustCompile(`(?m)^## `).FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

// TestSDLCCheckConstraintMatchesContract verifies that the runs.status CHECK
// constraint and the machine-readable contract list the same 15 states in the
// same order.
func TestSDLCCheckConstraintMatchesContract(t *testing.T) {
	contract := loadSDLCContract(t)

	migration := readRepoFile(t, "migrations/postgres/0002_init_tasks_and_sdlc.up.sql")
	checkRe := regexp.MustCompile(`(?s)status TEXT NOT NULL DEFAULT 'idea' CHECK \(status IN \((.*?)\)\)`)
	match := checkRe.FindStringSubmatch(migration)
	require.Len(t, match, 2, "runs.status CHECK constraint not found")

	valueRe := regexp.MustCompile(`'([a-z_]+)'`)
	values := valueRe.FindAllStringSubmatch(match[1], -1)
	require.Len(t, values, 15, "runs.status CHECK must contain 15 values")

	checkStates := make([]string, 0, len(values))
	for _, value := range values {
		checkStates = append(checkStates, value[1])
	}

	contractStates := make([]string, 0, len(contract.States))
	labels := make(map[string]string, len(contract.States))
	for _, state := range contract.States {
		contractStates = append(contractStates, state.ID)
		labels[state.Label] = state.ID
	}

	assert.Equal(t, checkStates, contractStates,
		"contract states must match the runs.status CHECK constraint order")
	assert.Len(t, labels, 15, "state labels must be unique")

	for _, state := range contract.States {
		assert.NotEmpty(t, state.Label, "state %q has no display label", state.ID)
	}
}

// TestSDLCContractTransitions verifies the transition graph and the enums of
// the machine-readable table.
func TestSDLCContractTransitions(t *testing.T) {
	contract := loadSDLCContract(t)
	require.Len(t, contract.Transitions, 15, "the contract must define 15 transitions")

	knownStates := make(map[string]bool, len(contract.States))
	for _, state := range contract.States {
		knownStates[state.ID] = true
	}

	allowedInitiators := map[string]bool{
		"run_workflow":      true,
		"human_approval":    true,
		"external_evidence": true,
		"scheduled":         true,
	}
	allowedEscalations := map[string]bool{
		"none":              true,
		"max_rounds":        true,
		"max_steps_or_cost": true,
	}

	outgoing := make(map[string]string, len(contract.Transitions))
	seenIDs := make(map[string]bool, len(contract.Transitions))
	for _, transition := range contract.Transitions {
		assert.True(t, knownStates[transition.From], "%s: unknown from state %q", transition.ID, transition.From)
		assert.True(t, knownStates[transition.To], "%s: unknown to state %q", transition.ID, transition.To)
		assert.NotEmpty(t, transition.Condition, "%s: condition is required", transition.ID)
		assert.True(t, allowedInitiators[transition.Initiator],
			"%s: unknown initiator %q", transition.ID, transition.Initiator)
		assert.Equal(t, "stay_in_state", transition.Error.Status,
			"%s: error.status must be stay_in_state", transition.ID)
		assert.True(t, transition.Error.Audit, "%s: error.audit must be true", transition.ID)
		assert.True(t, allowedEscalations[transition.Error.Escalation],
			"%s: unknown escalation %q", transition.ID, transition.Error.Escalation)

		assert.False(t, seenIDs[transition.ID], "duplicate transition ID %s", transition.ID)
		seenIDs[transition.ID] = true
		assert.NotContains(t, outgoing, transition.From,
			"state %q has more than one outgoing transition", transition.From)
		outgoing[transition.From] = transition.To
	}

	// 14 forward transitions plus Monitoring -> Research.
	stateOrder := []string{
		"idea", "research", "requirements", "requirements_review", "design",
		"architecture", "architecture_review", "planning", "in_dev",
		"code_review", "qa", "build", "release", "deploy", "monitoring",
	}
	for i := 0; i < len(stateOrder)-1; i++ {
		assert.Equal(t, stateOrder[i+1], outgoing[stateOrder[i]],
			"forward transition out of %q", stateOrder[i])
	}
	assert.Equal(t, "research", outgoing["monitoring"], "feedback loop Monitoring -> Research")
	assert.Len(t, outgoing, 15, "every state except the feedback target must have one outgoing transition")
}

// TestSDLCContractMatchesStateMachineDoc verifies that section 3 of
// SDLC_STATE_MACHINE.md mirrors the machine-readable table and that every gate
// used by the contract exists in section 4 and in the Go gate registry.
func TestSDLCContractMatchesStateMachineDoc(t *testing.T) {
	contract := loadSDLCContract(t)
	doc := readRepoFile(t, "docs/architecture/SDLC_STATE_MACHINE.md")

	section3 := extractMarkdownSection(t, doc, "## 3. Transitions and conditions")
	rowRe := regexp.MustCompile(`(?m)^\|\s*(3\.\d+)\s*\|(.+)$`)
	rows := rowRe.FindAllStringSubmatch(section3, -1)
	require.Len(t, rows, 15, "section 3 must contain 15 transition rows")

	labelToID := make(map[string]string, len(contract.States))
	for _, state := range contract.States {
		labelToID[state.Label] = state.ID
	}

	// Human-readable cells of section 3 mapped to the machine-readable values.
	initiatorLabels := map[string]string{
		"run workflow":                  "run_workflow",
		"run workflow (reviewer agent)": "run_workflow",
		"human approval":                "human_approval",
		"external evidence":             "external_evidence",
		"scheduled":                     "scheduled",
	}
	gateCellRe := regexp.MustCompile("^`([a-z_]+)`$")

	type markdownTransition struct {
		from      string
		to        string
		initiator string
		gate      string
		errStatus string
		errAudit  bool
		errEsc    string
	}
	markdown := make(map[string]markdownTransition, len(rows))
	for _, row := range rows {
		id := row[1]
		// Columns: transition | initiator | condition | gate | on error.
		cells := strings.Split(row[2], "|")
		require.Len(t, cells, 6, "section 3 row %s: expected 5 data columns plus the trailing empty part", id)

		fromLabel, toLabel, ok := strings.Cut(strings.TrimSpace(cells[0]), "→")
		require.True(t, ok, "section 3 row %s: transition cell has no arrow", id)
		from, ok := labelToID[strings.TrimSpace(fromLabel)]
		require.True(t, ok, "section 3 row %s: unknown from label %q", id, fromLabel)
		to, ok := labelToID[strings.TrimSpace(toLabel)]
		require.True(t, ok, "section 3 row %s: unknown to label %q", id, toLabel)

		initiatorLabel := strings.TrimSpace(cells[1])
		initiator, ok := initiatorLabels[initiatorLabel]
		require.True(t, ok, "section 3 row %s: unknown initiator %q", id, initiatorLabel)

		gate := ""
		gateCell := strings.TrimSpace(cells[3])
		if gateCell != "—" {
			gateMatch := gateCellRe.FindStringSubmatch(gateCell)
			require.Len(t, gateMatch, 2, "section 3 row %s: malformed gate cell %q", id, gateCell)
			gate = gateMatch[1]
		}

		onError := strings.TrimSpace(cells[4])
		require.True(t, strings.HasPrefix(onError, "Stay in "),
			"section 3 row %s: on-error must start with %q, got %q", id, "Stay in", onError)
		errAudit := strings.Contains(onError, "; audit;")
		require.True(t, errAudit, "section 3 row %s: on-error must contain %q", id, "; audit;")

		var errEsc string
		switch {
		case strings.Contains(onError, "no escalation"):
			errEsc = "none"
		case strings.Contains(onError, "max_rounds"):
			errEsc = "max_rounds"
		case strings.Contains(onError, "`max_steps`") && strings.Contains(onError, "`max_cost_usd`"):
			errEsc = "max_steps_or_cost"
		default:
			require.Failf(t, "unknown escalation",
				"section 3 row %s: on-error cell %q has no recognised escalation", id, onError)
		}

		require.NotContains(t, markdown, id, "duplicate section 3 row %s", id)
		markdown[id] = markdownTransition{
			from:      from,
			to:        to,
			initiator: initiator,
			gate:      gate,
			errStatus: "stay_in_state",
			errAudit:  errAudit,
			errEsc:    errEsc,
		}
	}

	for _, transition := range contract.Transitions {
		md, ok := markdown[transition.ID]
		require.True(t, ok, "transition %s is missing from section 3", transition.ID)
		assert.Equal(t, transition.From, md.from, "%s: from state differs", transition.ID)
		assert.Equal(t, transition.To, md.to, "%s: to state differs", transition.ID)
		assert.Equal(t, transition.Initiator, md.initiator, "%s: initiator differs", transition.ID)
		expectedGate := ""
		if transition.Gate != nil {
			expectedGate = *transition.Gate
		}
		assert.Equal(t, expectedGate, md.gate, "%s: gate differs", transition.ID)
		assert.Equal(t, transition.Error.Status, md.errStatus, "%s: error.status differs", transition.ID)
		assert.Equal(t, transition.Error.Audit, md.errAudit, "%s: error.audit differs", transition.ID)
		assert.Equal(t, transition.Error.Escalation, md.errEsc, "%s: error.escalation differs", transition.ID)
	}
	assert.Len(t, markdown, len(contract.Transitions), "section 3 has rows not present in the contract")

	section4 := extractMarkdownSection(t, doc, "## 4. Gates")
	docGateRe := regexp.MustCompile("(?m)^\\|\\s*`([a-z_]+)`\\s*\\|")
	documentedGates := make(map[string]bool)
	for _, match := range docGateRe.FindAllStringSubmatch(section4, -1) {
		documentedGates[match[1]] = true
	}

	registry := readRepoFile(t, "apps/worker/internal/gates/registry.go")
	registryGateRe := regexp.MustCompile(`Gate\w+\s*=\s*"([a-z_]+)"`)
	registryGates := make(map[string]bool)
	for _, match := range registryGateRe.FindAllStringSubmatch(registry, -1) {
		registryGates[match[1]] = true
	}

	for _, transition := range contract.Transitions {
		if transition.Gate == nil {
			continue
		}
		assert.True(t, documentedGates[*transition.Gate],
			"%s: gate %q is not documented in section 4", transition.ID, *transition.Gate)
		assert.True(t, registryGates[*transition.Gate],
			"%s: gate %q is not defined in apps/worker/internal/gates", transition.ID, *transition.Gate)
	}
}

// TestRunEventsProtoStageTransitionPayload guards the STAGE_TRANSITION payload
// shape and field numbers against accidental renumbering. The field checks are
// scoped to the StageTransitionPayload body: the RunEvent envelope also
// declares run_id, so a whole-file search would not notice the field being
// removed from the payload.
func TestRunEventsProtoStageTransitionPayload(t *testing.T) {
	proto := readRepoFile(t, "packages/contracts/events/run_events.proto")

	assert.Contains(t, proto, "RUN_EVENT_TYPE_STAGE_TRANSITION = 4;")

	payloadRe := regexp.MustCompile(`(?s)message StageTransitionPayload \{(.*?)\n\}`)
	match := payloadRe.FindStringSubmatch(proto)
	require.Len(t, match, 2, "message StageTransitionPayload not found in run_events.proto")
	payload := match[1]

	for _, field := range []string{
		"string run_id = 1;",
		"string stage_run_id = 2;",
		"string from = 3;",
		"string to = 4;",
		"string actor = 5;",
		"string gate = 6;",
		"string reason = 7;",
	} {
		assert.Contains(t, payload, field)
	}
}
