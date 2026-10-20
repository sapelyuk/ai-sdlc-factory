package iam

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestJWTService_GenerateAndValidate(t *testing.T) {
	svc := NewJWTService("test-secret-key", 24*time.Hour)
	userID := uuid.New().String()
	wsID := uuid.New().String()
	role := "admin"

	token, err := svc.GenerateToken(userID, wsID, role)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken() returned empty token")
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("claims.UserID = %q, want %q", claims.UserID, userID)
	}
	if claims.WorkspaceID != wsID {
		t.Errorf("claims.WorkspaceID = %q, want %q", claims.WorkspaceID, wsID)
	}
	if claims.Role != role {
		t.Errorf("claims.Role = %q, want %q", claims.Role, role)
	}
}

func TestJWTService_ExpiredToken(t *testing.T) {
	svc := NewJWTService("test-secret-key", -1*time.Second)
	token, err := svc.GenerateToken(uuid.New().String(), uuid.New().String(), "viewer")
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	_, err = svc.ValidateToken(token)
	if err != ErrTokenExpired {
		t.Fatalf("ValidateToken() error = %v, want ErrTokenExpired", err)
	}
}

func TestJWTService_InvalidToken(t *testing.T) {
	svc := NewJWTService("test-secret-key", 24*time.Hour)

	_, err := svc.ValidateToken("not-a-jwt")
	if err != ErrTokenMalformed {
		t.Fatalf("ValidateToken() error = %v, want ErrTokenMalformed", err)
	}
}

func TestJWTService_WrongSecret(t *testing.T) {
	svc1 := NewJWTService("secret-1", 24*time.Hour)
	svc2 := NewJWTService("secret-2", 24*time.Hour)

	token, _ := svc1.GenerateToken(uuid.New().String(), uuid.New().String(), "admin")
	_, err := svc2.ValidateToken(token)
	if err != ErrTokenMalformed {
		t.Fatalf("ValidateToken() with wrong secret error = %v, want ErrTokenMalformed", err)
	}
}

func TestRoleLevel(t *testing.T) {
	tests := []struct {
		role  string
		level int
	}{
		{"owner", 4},
		{"admin", 3},
		{"operator", 2},
		{"viewer", 1},
		{"unknown", 0},
	}
	for _, test := range tests {
		if got := RoleLevel(test.role); got != test.level {
			t.Errorf("RoleLevel(%q) = %d, want %d", test.role, got, test.level)
		}
	}
}
