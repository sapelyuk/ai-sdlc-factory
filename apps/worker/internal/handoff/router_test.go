package handoff

import (
	"encoding/json"
	"errors"
	"testing"
)

func testTeam() *TeamConfig {
	return &TeamConfig{
		ID:       "team-1",
		Topology: TopologyOrchestrated,
		Lead:     &TeamMember{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
		Members: []TeamMember{
			{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
			{AgentID: "agent-architect", RoleName: "software-architect", Profile: "arch-strong"},
			{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
			{AgentID: "agent-qa", RoleName: "qa-engineer", Profile: "code-fast"},
		},
		ReviewMatrix: ReviewMatrix{
			RequiredReviewers: 1,
			AllowedRoles:      []string{"software-architect", "qa-engineer"},
		},
	}
}

func TestTeamConfig_FindMember(t *testing.T) {
	team := testTeam()
	if m := team.FindMember("agent-dev"); m == nil || m.RoleName != "software-engineer" {
		t.Fatalf("FindMember(agent-dev) = %+v, want software-engineer member", m)
	}
	if team.FindMember("agent-unknown") != nil {
		t.Fatal("FindMember(unknown) should return nil")
	}
}

func TestTeamConfig_FindReviewer(t *testing.T) {
	team := testTeam()
	if m := team.FindReviewer(); m == nil || m.AgentID != "agent-architect" {
		t.Fatalf("FindReviewer() = %+v, want agent-architect (first allowed role)", m)
	}

	team2 := &TeamConfig{
		Members:      []TeamMember{{AgentID: "a", RoleName: "software-engineer"}},
		ReviewMatrix: ReviewMatrix{AllowedRoles: []string{"qa-engineer"}},
	}
	if team2.FindReviewer() != nil {
		t.Fatal("FindReviewer() should return nil when no role is allowed")
	}
}

func TestRouterFor(t *testing.T) {
	if r, err := RouterFor(TopologyPipeline); err != nil {
		t.Fatalf("RouterFor(sequential): %v", err)
	} else if _, ok := r.(PipelineRouter); !ok {
		t.Fatalf("RouterFor(sequential) returned %T, want PipelineRouter", r)
	}
	if r, err := RouterFor(TopologyOrchestrated); err != nil {
		t.Fatalf("RouterFor(orchestrated): %v", err)
	} else if _, ok := r.(OrchestratedRouter); !ok {
		t.Fatalf("RouterFor(orchestrated) returned %T, want OrchestratedRouter", r)
	}
	if _, err := RouterFor("collaborative"); err == nil {
		t.Fatal("RouterFor(collaborative) should fail: no routing contract for that topology")
	}
}

func TestPipelineRouter_Route(t *testing.T) {
	team := testTeam()
	// The pipeline topology ignores the lead: routing follows the package.
	team.Lead = nil

	t.Run("nil package and team", func(t *testing.T) {
		router := PipelineRouter{}
		if _, err := router.Route(nil, team); !errors.Is(err, ErrNilPackage) {
			t.Fatalf("nil package: got %v, want ErrNilPackage", err)
		}
		pkg := &HandoffPackage{}
		if _, err := router.Route(pkg, nil); !errors.Is(err, ErrNilTeam) {
			t.Fatalf("nil team: got %v, want ErrNilTeam", err)
		}
	})

	t.Run("missing source", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{}
		if _, err := router.Route(pkg, team); !errors.Is(err, ErrUnknownSource) {
			t.Fatalf("no metadata: got %v, want ErrUnknownSource", err)
		}
		pkg.Metadata = &HandoffMetadata{SourceAgentID: "agent-outside"}
		if _, err := router.Route(pkg, team); !errors.Is(err, ErrUnknownSource) {
			t.Fatalf("outside source: got %v, want ErrUnknownSource", err)
		}
	})

	t.Run("explicit suggestion wins", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{
			SourceAgentID:        "agent-pm",
			SuggestedNextAgentID: "agent-dev",
		}}
		res, err := router.Route(pkg, team)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Terminal || len(res.NextAgentIDs) != 1 || res.NextAgentIDs[0] != "agent-dev" {
			t.Fatalf("route = %+v, want next [agent-dev]", res)
		}
	})

	t.Run("self suggestion is rejected", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{
			SourceAgentID:        "agent-dev",
			SuggestedNextAgentID: "agent-dev",
		}}
		if _, err := router.Route(pkg, team); !errors.Is(err, ErrUnknownAgent) {
			t.Fatalf("self route: got %v, want ErrUnknownAgent", err)
		}
	})

	t.Run("suggestion outside the team is rejected", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{
			SourceAgentID:        "agent-dev",
			SuggestedNextAgentID: "agent-outside",
		}}
		if _, err := router.Route(pkg, team); !errors.Is(err, ErrUnknownAgent) {
			t.Fatalf("outside suggestion: got %v, want ErrUnknownAgent", err)
		}
	})

	t.Run("review matrix picks the next reviewer", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-dev"}}
		res, err := router.Route(pkg, team)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Terminal || len(res.NextAgentIDs) != 1 || res.NextAgentIDs[0] != "agent-architect" {
			t.Fatalf("route = %+v, want next [agent-architect]", res)
		}
	})

	t.Run("reviewer finishing the review completes the chain", func(t *testing.T) {
		router := PipelineRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-qa"}}
		res, err := router.Route(pkg, team)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Terminal || len(res.NextAgentIDs) != 0 {
			t.Fatalf("route = %+v, want terminal", res)
		}
	})

	t.Run("no review required completes the chain", func(t *testing.T) {
		router := PipelineRouter{}
		noReview := testTeam()
		noReview.ReviewMatrix = ReviewMatrix{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-dev"}}
		res, err := router.Route(pkg, noReview)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Terminal {
			t.Fatalf("route = %+v, want terminal without review matrix", res)
		}
	})
}

func TestOrchestratedRouter_Route(t *testing.T) {
	team := testTeam()

	t.Run("nil package and team", func(t *testing.T) {
		router := OrchestratedRouter{}
		if _, err := router.Route(nil, team); !errors.Is(err, ErrNilPackage) {
			t.Fatalf("nil package: got %v, want ErrNilPackage", err)
		}
		pkg := &HandoffPackage{}
		if _, err := router.Route(pkg, nil); !errors.Is(err, ErrNilTeam) {
			t.Fatalf("nil team: got %v, want ErrNilTeam", err)
		}
	})

	t.Run("missing source", func(t *testing.T) {
		router := OrchestratedRouter{}
		pkg := &HandoffPackage{}
		if _, err := router.Route(pkg, team); !errors.Is(err, ErrUnknownSource) {
			t.Fatalf("no metadata: got %v, want ErrUnknownSource", err)
		}
	})

	t.Run("team without lead", func(t *testing.T) {
		router := OrchestratedRouter{}
		noLead := testTeam()
		noLead.Lead = nil
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-dev"}}
		if _, err := router.Route(pkg, noLead); !errors.Is(err, ErrNoLead) {
			t.Fatalf("no lead: got %v, want ErrNoLead", err)
		}
	})

	t.Run("lead package is terminal", func(t *testing.T) {
		router := OrchestratedRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-pm"}}
		res, err := router.Route(pkg, team)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Terminal || len(res.NextAgentIDs) != 0 {
			t.Fatalf("route = %+v, want terminal for the lead package", res)
		}
	})

	t.Run("member package routes through the lead", func(t *testing.T) {
		router := OrchestratedRouter{}
		pkg := &HandoffPackage{Metadata: &HandoffMetadata{SourceAgentID: "agent-architect"}}
		res, err := router.Route(pkg, team)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Terminal || len(res.NextAgentIDs) != 1 || res.NextAgentIDs[0] != "agent-pm" {
			t.Fatalf("route = %+v, want next [agent-pm]", res)
		}
	})
}

// TestMetadata_JSONRoundTrip guards the on-wire absence of the runtime
// header: a package marshalled without metadata must not carry a metadata
// key, so the wire schema (additionalProperties=false) accepts it.
func TestMetadata_JSONRoundTrip(t *testing.T) {
	pkg := &HandoffPackage{
		Goal:                 "g",
		ContextRefs:          []string{"sem://x"},
		AcceptanceCriteria:   []string{"c"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["metadata"]; ok {
		t.Fatal("wire form must not contain a metadata key")
	}
}
