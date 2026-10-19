package domain

import (
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

// Topology represents the team topology.
type Topology string

const (
	TopologyOrchestrated  Topology = "orchestrated"
	TopologyCollaborative Topology = "collaborative"
	TopologySequential    Topology = "sequential"
)

// TeamMemberSpec defines a member of a team.
type TeamMemberSpec struct {
	RoleName    string `json:"role_name" yaml:"role_name"`
	RoleVersion string `json:"role_version" yaml:"role_version"`
	Count       int    `json:"count,omitempty" yaml:"count,omitempty"`
}

// BudgetSpec defines budget configuration.
type BudgetSpec struct {
	MaxCostUSD float64 `json:"max_cost_usd" yaml:"max_cost_usd"`
	MaxSteps   int     `json:"max_steps" yaml:"max_steps"`
}

// ReviewMatrixSpec defines the review matrix for a team.
type ReviewMatrixSpec struct {
	RequiredReviewers int      `json:"required_reviewers" yaml:"required_reviewers"`
	AllowedRoles      []string `json:"allowed_roles,omitempty" yaml:"allowed_roles,omitempty"`
}

// TeamSpec is the YAML-serializable specification for a Team.
type TeamSpec struct {
	Description     string           `json:"description" yaml:"description"`
	Topology        Topology         `json:"topology" yaml:"topology"`
	Members         []TeamMemberSpec `json:"members" yaml:"members"`
	Budget          BudgetSpec       `json:"budget" yaml:"budget"`
	ReviewMatrix    ReviewMatrixSpec `json:"review_matrix" yaml:"review_matrix"`
	ProcessRef      string           `json:"process_ref,omitempty" yaml:"process_ref,omitempty"`
	KnowledgeScopes []string         `json:"knowledge_scopes,omitempty" yaml:"knowledge_scopes,omitempty"`
}

// Team is a versioned registry entity.
type Team struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Name        string          `json:"name"`
	Version     *semver.Version `json:"version"`
	Spec        TeamSpec        `json:"spec"`
	Status      Status          `json:"status"`
	AuthorID    uuid.UUID       `json:"author_id"`
	ApproverID  *uuid.UUID      `json:"approver_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// NewTeam creates a new Team with version 1.0.0.
func NewTeam(workspaceID, authorID uuid.UUID, name string, spec TeamSpec) *Team {
	return &Team{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        name,
		Version:     semver.MustParse("1.0.0"),
		Spec:        spec,
		Status:      StatusDraft,
		AuthorID:    authorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func (t *Team) VersionString() string {
	if t.Version == nil {
		return "1.0.0"
	}
	return t.Version.String()
}

// Clone creates a new Team as a minor version clone.
func (t *Team) Clone() *Team {
	newVersion := t.version().IncMinor()
	return &Team{
		ID:          uuid.New(),
		WorkspaceID: t.WorkspaceID,
		Name:        t.Name,
		Version:     &newVersion,
		Spec:        cloneTeamSpec(t.Spec),
		Status:      StatusDraft,
		AuthorID:    t.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// Revert creates a new Team with the content of the previous version.
func (t *Team) Revert(prevSpec TeamSpec) *Team {
	newVersion := t.version().IncMinor()
	return &Team{
		ID:          uuid.New(),
		WorkspaceID: t.WorkspaceID,
		Name:        t.Name,
		Version:     &newVersion,
		Spec:        cloneTeamSpec(prevSpec),
		Status:      StatusDraft,
		AuthorID:    t.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func (t *Team) version() *semver.Version {
	if t.Version == nil {
		return semver.MustParse("1.0.0")
	}
	return t.Version
}

func cloneTeamSpec(spec TeamSpec) TeamSpec {
	spec.Members = append([]TeamMemberSpec(nil), spec.Members...)
	spec.ReviewMatrix.AllowedRoles = append([]string(nil), spec.ReviewMatrix.AllowedRoles...)
	spec.KnowledgeScopes = append([]string(nil), spec.KnowledgeScopes...)
	return spec
}
