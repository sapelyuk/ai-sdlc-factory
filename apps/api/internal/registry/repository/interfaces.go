// Package repository defines repository interfaces and PostgreSQL implementations for the Registry.
package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type RoleRepository interface {
	Create(context.Context, *domain.Role) error
	GetByID(context.Context, uuid.UUID) (*domain.Role, error)
	GetByNameAndVersion(context.Context, string, string) (*domain.Role, error)
	List(context.Context, string, string) ([]*domain.Role, error)
	Update(context.Context, uuid.UUID, domain.RoleSpec) error
	UpdateStatus(context.Context, uuid.UUID, domain.Status, uuid.UUID) error
	Clone(context.Context, uuid.UUID) (*domain.Role, error)
	Revert(context.Context, uuid.UUID) (*domain.Role, error)
}

type TeamRepository interface {
	Create(context.Context, *domain.Team) error
	GetByID(context.Context, uuid.UUID) (*domain.Team, error)
	GetByNameAndVersion(context.Context, string, string) (*domain.Team, error)
	List(context.Context, string) ([]*domain.Team, error)
	Update(context.Context, uuid.UUID, domain.TeamSpec) error
	UpdateStatus(context.Context, uuid.UUID, domain.Status, uuid.UUID) error
	Clone(context.Context, uuid.UUID) (*domain.Team, error)
	Revert(context.Context, uuid.UUID) (*domain.Team, error)
}

type ProcessRepository interface {
	Create(context.Context, *domain.Process) error
	GetByID(context.Context, uuid.UUID) (*domain.Process, error)
	GetByNameAndVersion(context.Context, string, string) (*domain.Process, error)
	List(context.Context, string) ([]*domain.Process, error)
	Update(context.Context, uuid.UUID, domain.ProcessSpec) error
	UpdateStatus(context.Context, uuid.UUID, domain.Status, uuid.UUID) error
	Clone(context.Context, uuid.UUID) (*domain.Process, error)
	Revert(context.Context, uuid.UUID) (*domain.Process, error)
}
