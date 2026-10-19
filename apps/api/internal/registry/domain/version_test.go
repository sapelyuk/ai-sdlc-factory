package domain

import (
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

func TestRoleCloneAndRevertCreateIndependentMinorVersions(t *testing.T) {
	workspaceID, authorID := uuid.New(), uuid.New()
	role := NewRole(workspaceID, authorID, "backend-developer", CategoryEngineering, RoleSpec{
		Description: "original",
		Tools:       ToolsSpec{Allow: []string{"fs.read"}},
	})
	clone := role.Clone()
	if got, want := clone.Version.String(), "1.1.0"; got != want {
		t.Fatalf("clone version = %s, want %s", got, want)
	}
	if clone.Status != StatusDraft {
		t.Fatalf("clone status = %s, want draft", clone.Status)
	}
	clone.Spec.Tools.Allow[0] = "git.commit"
	if role.Spec.Tools.Allow[0] != "fs.read" {
		t.Fatal("clone shares mutable spec slices with its source")
	}

	reverted := clone.Revert(role.Spec)
	if got, want := reverted.Version.String(), "1.2.0"; got != want {
		t.Fatalf("revert version = %s, want %s", got, want)
	}
	if reverted.Spec.Description != "original" || reverted.Status != StatusDraft {
		t.Fatalf("revert did not restore the source spec as a draft: %#v", reverted)
	}
}

func TestTeamCloneAndRevertCreateMinorVersions(t *testing.T) {
	team := NewTeam(uuid.New(), uuid.New(), "mvp-startup", TeamSpec{
		Members: []TeamMemberSpec{{RoleName: "qa-engineer", Count: 1}},
	})
	clone := team.Clone()
	if got, want := clone.VersionString(), "1.1.0"; got != want {
		t.Fatalf("clone version = %s, want %s", got, want)
	}
	clone.Spec.Members[0].Count = 2
	if team.Spec.Members[0].Count != 1 {
		t.Fatal("clone shares mutable members with its source")
	}
	reverted := clone.Revert(team.Spec)
	if got, want := reverted.VersionString(), "1.2.0"; got != want {
		t.Fatalf("revert version = %s, want %s", got, want)
	}
	if reverted.Status != StatusDraft {
		t.Fatalf("revert status = %s, want draft", reverted.Status)
	}
}

func TestProcessCloneAndRevertCreateMinorVersions(t *testing.T) {
	process := NewProcess(uuid.New(), uuid.New(), "default-sdlc", ProcessSpec{
		Stages: []StageSpec{{Name: "Idea", Agents: []string{"product-manager"}}},
	})
	clone := process.Clone()
	if got, want := clone.VersionString(), "1.1.0"; got != want {
		t.Fatalf("clone version = %s, want %s", got, want)
	}
	clone.Spec.Stages[0].Agents[0] = "backend-developer"
	if process.Spec.Stages[0].Agents[0] != "product-manager" {
		t.Fatal("clone shares nested mutable stage data with its source")
	}
	reverted := clone.Revert(process.Spec)
	if got, want := reverted.VersionString(), "1.2.0"; got != want {
		t.Fatalf("revert version = %s, want %s", got, want)
	}
	if reverted.Status != StatusDraft {
		t.Fatalf("revert status = %s, want draft", reverted.Status)
	}
}

func TestStatusCanTransition(t *testing.T) {
	tests := []struct {
		from Status
		to   Status
		want bool
	}{
		{StatusDraft, StatusApproved, true},
		{StatusDraft, StatusDeprecated, true},
		{StatusApproved, StatusDeprecated, true},
		{StatusApproved, StatusDraft, false},
		{StatusDeprecated, StatusApproved, false},
	}
	for _, test := range tests {
		if got := test.from.CanTransition(test.to); got != test.want {
			t.Errorf("%s -> %s = %t, want %t", test.from, test.to, got, test.want)
		}
	}
}

func TestRoleVersionStringUsesValidDefaultWhenUnset(t *testing.T) {
	role := &Role{}
	if got, want := role.VersionString(), semver.MustParse("1.0.0").String(); got != want {
		t.Fatalf("VersionString() = %s, want %s", got, want)
	}
}
