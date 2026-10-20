package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"golang.org/x/crypto/bcrypt"
)

// LoginHandler handles POST /api/v1/auth/login.
type LoginHandler struct {
	users      iam.UserRepository
	jwtService *iam.JWTService
	validate   *validator.Validate
}

// NewLoginHandler creates a new LoginHandler.
func NewLoginHandler(users iam.UserRepository, jwtService *iam.JWTService) *LoginHandler {
	return &LoginHandler{
		users:      users,
		jwtService: jwtService,
		validate:   validator.New(),
	}
}

// Login authenticates a user and returns a JWT.
func (h *LoginHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req iam.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}

	// go-playground/validator validation.
	if err := h.validate.Struct(req); err != nil {
		details := make(map[string]string)
		for _, e := range err.(validator.ValidationErrors) {
			details[e.Field()] = e.Tag()
		}
		writeValidationError(w, details)
		return
	}

	user, err := h.users.GetByEmail(r.Context(), req.Email)
	if err != nil {
		slog.Debug("login: user not found", "email", req.Email)
		jsonError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		jsonError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	token, err := h.jwtService.GenerateToken(user.ID, user.WorkspaceID, user.Role)
	if err != nil {
		slog.Error("failed to generate JWT", "error", err)
		jsonError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := iam.LoginResponse{
		Token: token,
		User: iam.UserResponse{
			ID:    user.ID,
			Email: user.Email,
			Role:  user.Role,
		},
	}
	jsonResponse(w, http.StatusOK, resp)
}

// writeValidationError writes a 422 response with validation error details.
func writeValidationError(w http.ResponseWriter, details map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]any{
		"error":   "validation failed",
		"details": details,
	})
}
