// Package iam provides identity and access management.
package iam

import "time"

// ValidRoles lists all roles in hierarchy order (lowest to highest).
var ValidRoles = []string{"owner", "admin", "operator", "viewer"}

// RoleLevel returns the numeric level for a role (higher = more permissions).
func RoleLevel(role string) int {
	switch role {
	case "owner":
		return 4
	case "admin":
		return 3
	case "operator":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

// User represents an authenticated user in the system.
type User struct {
	ID           string    `db:"id" json:"id"`
	WorkspaceID  string    `db:"workspace_id" json:"workspace_id"`
	Email        string    `db:"email" json:"email"`
	PasswordHash string    `db:"password_hash" json:"-"`
	Role         string    `db:"role" json:"role"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

// UserResponse is the public user info returned after login.
type UserResponse struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// LoginRequest is the DTO for the login endpoint.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=1"`
}

// LoginResponse is the DTO returned after successful login.
type LoginResponse struct {
	Token string        `json:"token"`
	User  UserResponse  `json:"user"`
}
