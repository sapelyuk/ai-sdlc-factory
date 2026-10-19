// Package domain defines the core domain models for the Registry.
package domain

import (
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

// Status represents the lifecycle status of a registry entity.
type Status string

const (
	StatusDraft      Status = "draft"
	StatusApproved   Status = "approved"
	StatusDeprecated Status = "deprecated"
)

// AutonomyLevel represents the autonomy level of a role.
type AutonomyLevel string

const (
	AutonomyStrict     AutonomyLevel = "strict"
	AutonomyBalanced   AutonomyLevel = "balanced"
	AutonomyAutonomous AutonomyLevel = "autonomous"
)

// Category represents the category of a role.
type Category string

const (
	CategoryProduct     Category = "product"
	CategoryDesign      Category = "design"
	CategoryEngineering Category = "engineering"
	CategoryQA          Category = "qa"
	CategoryDevOps      Category = "devops"
	CategoryResearch    Category = "research"
	CategorySecurity    Category = "security"
)

func (c Category) IsValid() bool {
	switch c {
	case CategoryProduct, CategoryDesign, CategoryEngineering, CategoryQA,
		CategoryDevOps, CategoryResearch, CategorySecurity:
		return true
	default:
		return false
	}
}

// ArtifactSpec defines input/output artifact specification.
type ArtifactSpec struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// ToolsSpec defines allowed/denied tools.
type ToolsSpec struct {
	Allow []string `json:"allow" yaml:"allow"`
	Deny  []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}

// ModelSpec defines model requirements.
type ModelSpec struct {
	Capability string `json:"capability" yaml:"capability"`
	MinTokens  int    `json:"min_tokens,omitempty" yaml:"min_tokens,omitempty"`
}

// KnowledgeSpec defines knowledge base configuration.
type KnowledgeSpec struct {
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// LimitsSpec defines execution limits.
type LimitsSpec struct {
	MaxSteps   int     `json:"max_steps" yaml:"max_steps"`
	MaxCostUSD float64 `json:"max_cost_usd" yaml:"max_cost_usd"`
}

// RoleSpec is the YAML-serializable specification for a Role.
type RoleSpec struct {
	Description     string         `json:"description" yaml:"description"`
	SystemPromptRef string         `json:"system_prompt_ref" yaml:"system_prompt_ref"`
	Inputs          []ArtifactSpec `json:"inputs" yaml:"inputs"`
	Outputs         []ArtifactSpec `json:"outputs" yaml:"outputs"`
	Tools           ToolsSpec      `json:"tools" yaml:"tools"`
	Model           ModelSpec      `json:"model" yaml:"model"`
	Knowledge       KnowledgeSpec  `json:"knowledge" yaml:"knowledge"`
	Limits          LimitsSpec     `json:"limits" yaml:"limits"`
	Autonomy        AutonomyLevel  `json:"autonomy" yaml:"autonomy"`
	QualityChecks   []string       `json:"quality_checks,omitempty" yaml:"quality_checks,omitempty"`
}

// Role is a versioned registry entity.
type Role struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Name        string          `json:"name"`
	Version     *semver.Version `json:"version"`
	Category    Category        `json:"category"`
	Spec        RoleSpec        `json:"spec"`
	Status      Status          `json:"status"`
	AuthorID    uuid.UUID       `json:"author_id"`
	ApproverID  *uuid.UUID      `json:"approver_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// VersionString returns the semver string representation.
func (r *Role) VersionString() string {
	if r.Version == nil {
		return "1.0.0"
	}
	return r.Version.String()
}

// NewRole creates a new Role with version 1.0.0.
func NewRole(workspaceID, authorID uuid.UUID, name string, category Category, spec RoleSpec) *Role {
	return &Role{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        name,
		Version:     semver.MustParse("1.0.0"),
		Category:    category,
		Spec:        spec,
		Status:      StatusDraft,
		AuthorID:    authorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// Clone creates a new Role as a minor version clone of the original.
func (r *Role) Clone() *Role {
	newVersion := r.version().IncMinor()
	return &Role{
		ID:          uuid.New(),
		WorkspaceID: r.WorkspaceID,
		Name:        r.Name,
		Version:     &newVersion,
		Category:    r.Category,
		Spec:        cloneRoleSpec(r.Spec),
		Status:      StatusDraft,
		AuthorID:    r.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// Revert creates a new Role with the content of the previous version.
func (r *Role) Revert(prevSpec RoleSpec) *Role {
	newVersion := r.version().IncMinor()
	return &Role{
		ID:          uuid.New(),
		WorkspaceID: r.WorkspaceID,
		Name:        r.Name,
		Version:     &newVersion,
		Category:    r.Category,
		Spec:        cloneRoleSpec(prevSpec),
		Status:      StatusDraft,
		AuthorID:    r.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func (r *Role) version() *semver.Version {
	if r.Version == nil {
		return semver.MustParse("1.0.0")
	}
	return r.Version
}

func cloneRoleSpec(spec RoleSpec) RoleSpec {
	spec.Inputs = append([]ArtifactSpec(nil), spec.Inputs...)
	spec.Outputs = append([]ArtifactSpec(nil), spec.Outputs...)
	spec.Tools.Allow = append([]string(nil), spec.Tools.Allow...)
	spec.Tools.Deny = append([]string(nil), spec.Tools.Deny...)
	spec.Knowledge.Scopes = append([]string(nil), spec.Knowledge.Scopes...)
	spec.QualityChecks = append([]string(nil), spec.QualityChecks...)
	return spec
}

// ErrImmutable is returned when attempting to modify an approved version.
var ErrImmutable = fmt.Errorf("approved versions are immutable; use clone or revert")

// ErrNotFound is returned when a registry entity is not found.
var ErrNotFound = fmt.Errorf("not found")

// ErrInvalidStatusTransition is returned for invalid status transitions.
var ErrInvalidStatusTransition = fmt.Errorf("invalid status transition")

// ErrApproverIsAuthor is returned when an author attempts to approve their own version.
var ErrApproverIsAuthor = fmt.Errorf("approver cannot be the author")

// CanTransition checks if a status transition is valid.
func (s Status) CanTransition(to Status) bool {
	switch s {
	case StatusDraft:
		return to == StatusApproved || to == StatusDeprecated
	case StatusApproved:
		return to == StatusDeprecated
	case StatusDeprecated:
		return false
	default:
		return false
	}
}
