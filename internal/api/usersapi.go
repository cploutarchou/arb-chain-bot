package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// UserAdmin is the console user & role management service (BL-11). The
// HTTP layer below enforces RBAC (PermUserManage) and CSRF; the
// implementation (internal/auth.AdminService, over pgx or the in-memory
// store) enforces the invariants that must hold regardless of caller —
// self-protection, last-admin protection, and session revocation on any
// credential or role change. Web and Telegram would share this same
// interface (SKILL §52, §56).
type UserAdmin interface {
	ListUsers(ctx context.Context) ([]auth.User, error)
	CreateUser(ctx context.Context, email string, role auth.Role, password string) (auth.User, error)
	UpdateUserRole(ctx context.Context, actorID, targetID string, role auth.Role) (auth.User, error)
	SetUserDisabled(ctx context.Context, actorID, targetID string, disabled bool) (auth.User, error)
	SetUserPassword(ctx context.Context, targetID, password string) (auth.User, error)
	ChangeOwnPassword(ctx context.Context, userID, currentPassword, newPassword string) error
}

// userDTO is the wire shape for auth.User. auth.User is never marshaled
// directly: it carries PasswordHash and has no json tags, so an
// accidental json.Marshal(auth.User{}) would leak a credential.
type userDTO struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserDTO(u auth.User) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, Role: string(u.Role), Disabled: u.Disabled, CreatedAt: u.CreatedAt}
}

// writeUserAdminError maps AdminService's structured sentinels to the
// API error schema (mirrors opsapi.go's errors.Is switch convention).
func (s *Server) writeUserAdminError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrUnknownUser):
		WriteError(w, http.StatusNotFound, "not_found", "user not found", correlationID(r))
	case errors.Is(err, auth.ErrDuplicateEmail):
		WriteError(w, http.StatusConflict, "duplicate_email", err.Error(), correlationID(r))
	case errors.Is(err, auth.ErrLastAdmin):
		WriteError(w, http.StatusConflict, "last_admin", err.Error(), correlationID(r))
	case errors.Is(err, auth.ErrSelfTarget):
		WriteError(w, http.StatusConflict, "self_target", err.Error(), correlationID(r))
	case errors.Is(err, auth.ErrWeakPassword),
		errors.Is(err, auth.ErrInvalidRole),
		errors.Is(err, auth.ErrInvalidEmail):
		WriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), correlationID(r))
	case errors.Is(err, auth.ErrPasswordMismatch):
		WriteError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", correlationID(r))
	case errors.Is(err, auth.ErrThrottled):
		WriteError(w, http.StatusTooManyRequests, "throttled", "too many attempts; try again later", correlationID(r))
	default:
		s.log.Error("user management failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "user_management_failed", "user management failed", correlationID(r))
	}
}

// usersRoutes: console user & role management (BL-11) plus the
// self-service password change every authenticated user gets regardless
// of role. Every mutation needs PermUserManage (ADMIN only) except
// changing your own password, and every mutation is CSRF-protected and
// audited.
func (s *Server) usersRoutes(mux *http.ServeMux) {
	needUsers := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Users == nil {
				WriteError(w, http.StatusNotFound, "users_absent", "user management not available in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("GET /api/v1/users", s.requirePerm(auth.PermUserManage, needUsers(func(w http.ResponseWriter, r *http.Request) {
		users, err := s.Users.ListUsers(r.Context())
		if err != nil {
			s.writeUserAdminError(w, r, err)
			return
		}
		dtos := make([]userDTO, 0, len(users))
		for _, u := range users {
			dtos = append(dtos, toUserDTO(u))
		}
		WriteData(w, http.StatusOK, map[string]any{"users": dtos})
	})))

	mux.HandleFunc("POST /api/v1/users", s.requirePerm(auth.PermUserManage, s.requireCSRF(needUsers(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email    string `json:"email"`
			Role     string `json:"role"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed user request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		u, err := s.Users.CreateUser(r.Context(), req.Email, auth.Role(req.Role), req.Password)
		if err != nil {
			s.writeUserAdminError(w, r, err)
			return
		}
		s.audit(r, p.UserID, "user.create", "user:"+u.ID)
		s.log.Info("user created", "actor", p.UserID, "user", u.ID, "role", string(u.Role))
		WriteData(w, http.StatusCreated, map[string]any{"user": toUserDTO(u)})
	}))))

	mux.HandleFunc("POST /api/v1/users/{id}/role", s.requirePerm(auth.PermUserManage, s.requireCSRF(needUsers(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed role request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		id := r.PathValue("id")
		u, err := s.Users.UpdateUserRole(r.Context(), p.UserID, id, auth.Role(req.Role))
		if err != nil {
			s.writeUserAdminError(w, r, err)
			return
		}
		s.audit(r, p.UserID, "user.role_change", "user:"+id)
		WriteData(w, http.StatusOK, map[string]any{"user": toUserDTO(u)})
	}))))

	disableEnable := func(action string, disabled bool) http.HandlerFunc {
		return s.requirePerm(auth.PermUserManage, s.requireCSRF(needUsers(func(w http.ResponseWriter, r *http.Request) {
			p, _ := PrincipalFrom(r.Context())
			id := r.PathValue("id")
			u, err := s.Users.SetUserDisabled(r.Context(), p.UserID, id, disabled)
			if err != nil {
				s.writeUserAdminError(w, r, err)
				return
			}
			s.audit(r, p.UserID, action, "user:"+id)
			WriteData(w, http.StatusOK, map[string]any{"user": toUserDTO(u)})
		})))
	}
	mux.HandleFunc("POST /api/v1/users/{id}/disable", disableEnable("user.disable", true))
	mux.HandleFunc("POST /api/v1/users/{id}/enable", disableEnable("user.enable", false))

	mux.HandleFunc("POST /api/v1/users/{id}/password", s.requirePerm(auth.PermUserManage, s.requireCSRF(needUsers(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed password request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		id := r.PathValue("id")
		u, err := s.Users.SetUserPassword(r.Context(), id, req.Password)
		if err != nil {
			s.writeUserAdminError(w, r, err)
			return
		}
		s.audit(r, p.UserID, "user.password_reset", "user:"+id)
		WriteData(w, http.StatusOK, map[string]any{"user": toUserDTO(u)})
	}))))

	// Self-service password change: any authenticated user, gated on
	// requireAuth only — everyone may change their own password.
	mux.HandleFunc("POST /api/v1/auth/password", s.requireAuth(s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		if s.Users == nil {
			WriteError(w, http.StatusNotFound, "users_absent", "user management not available in this profile", correlationID(r))
			return
		}
		var req struct {
			Current string `json:"current_password"`
			New     string `json:"new_password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed password request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		if err := s.Users.ChangeOwnPassword(r.Context(), p.UserID, req.Current, req.New); err != nil {
			s.writeUserAdminError(w, r, err)
			return
		}
		s.audit(r, p.UserID, "user.password_change", "user:"+p.UserID)
		WriteData(w, http.StatusOK, map[string]any{"status": "password_changed"})
	})))
}
