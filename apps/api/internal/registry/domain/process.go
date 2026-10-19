package domain

import (
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
)

// StageSpec defines a stage in a process.
type StageSpec struct {
	Name        string   `json:"name" yaml:"name"`
	Order       int      `json:"order" yaml:"order"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	Gate        string   `json:"gate" yaml:"gate"`
	Agents      []string `json:"agents,omitempty" yaml:"agents,omitempty"`
}

// ProcessSpec is the YAML-serializable specification for a Process.
type ProcessSpec struct {
	Description string      `json:"description" yaml:"description"`
	Stages      []StageSpec `json:"stages" yaml:"stages"`
	DefaultTeam string      `json:"default_team,omitempty" yaml:"default_team,omitempty"`
}

// Process is a versioned registry entity.
type Process struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	Name        string          `json:"name"`
	Version     *semver.Version `json:"version"`
	Spec        ProcessSpec     `json:"spec"`
	Status      Status          `json:"status"`
	AuthorID    uuid.UUID       `json:"author_id"`
	ApproverID  *uuid.UUID      `json:"approver_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// NewProcess creates a new Process with version 1.0.0.
func NewProcess(workspaceID, authorID uuid.UUID, name string, spec ProcessSpec) *Process {
	return &Process{
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

func (p *Process) VersionString() string {
	if p.Version == nil {
		return "1.0.0"
	}
	return p.Version.String()
}

// Clone creates a new Process as a minor version clone.
func (p *Process) Clone() *Process {
	newVersion := p.version().IncMinor()
	return &Process{
		ID:          uuid.New(),
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		Version:     &newVersion,
		Spec:        cloneProcessSpec(p.Spec),
		Status:      StatusDraft,
		AuthorID:    p.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// Revert creates a new Process with the content of the previous version.
func (p *Process) Revert(prevSpec ProcessSpec) *Process {
	newVersion := p.version().IncMinor()
	return &Process{
		ID:          uuid.New(),
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		Version:     &newVersion,
		Spec:        cloneProcessSpec(prevSpec),
		Status:      StatusDraft,
		AuthorID:    p.AuthorID,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func (p *Process) version() *semver.Version {
	if p.Version == nil {
		return semver.MustParse("1.0.0")
	}
	return p.Version
}

func cloneProcessSpec(spec ProcessSpec) ProcessSpec {
	spec.Stages = append([]StageSpec(nil), spec.Stages...)
	for i := range spec.Stages {
		spec.Stages[i].Agents = append([]string(nil), spec.Stages[i].Agents...)
	}
	return spec
}
