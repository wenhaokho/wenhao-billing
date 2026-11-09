package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/auth/authdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Python's sentences for the user routes.
const (
	emailInUse    = "email already in use"
	emailExists   = "email already exists"
	userNotFound  = "user not found"
	usersEmailKey = "users_email_key"
)

// UserProfileUpdate is the signed-in user's own edit. As in Python, null and
// omitted both leave a field alone; an empty display_name clears it.
type UserProfileUpdate struct {
	DisplayName *string         `json:"display_name,omitempty" maxLength:"255"`
	Email       *platform.Email `json:"email,omitempty" maxLength:"255"`
}

// UserCreate adds a user.
type UserCreate struct {
	Email       platform.Email `json:"email" maxLength:"255"`
	Password    string         `json:"password" minLength:"6" maxLength:"128"`
	Role        string         `json:"role,omitempty" maxLength:"20" doc:"Defaults to admin."`
	DisplayName *string        `json:"display_name,omitempty" maxLength:"255"`
}

// UserPasswordReset is an admin's new password for a user.
type UserPasswordReset struct {
	Password string `json:"password" minLength:"6" maxLength:"128"`
}

func registerUsers(api huma.API, r routes) {
	const prefix = "/api/v1/users"
	tags := []string{"users"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "users-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List users",
	}), r.listUsers)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "users-update-me", Method: http.MethodPatch, Path: prefix + "/me", Tags: tags,
		Summary: "Edit the signed-in user's profile", Errors: []int{http.StatusConflict},
	}), r.updateMe)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "users-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a user", DefaultStatus: http.StatusCreated, Errors: []int{http.StatusConflict},
	}), r.createUser)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "users-reset-password", Method: http.MethodPost, Path: prefix + "/{user_id}/reset-password", Tags: tags,
		Summary: "Set a user's password and sign them out everywhere", Errors: []int{http.StatusNotFound},
	}), r.adminResetPassword)
}

var emailConstraint = map[string]platform.Constraint{
	usersEmailKey: {Status: http.StatusConflict, Code: "email_in_use", Detail: emailInUse},
}

var emailExistsConstraint = map[string]platform.Constraint{
	usersEmailKey: {Status: http.StatusConflict, Code: "email_already_exists", Detail: emailExists},
}

type usersOut struct{ Body []UserOut }

func (r routes) listUsers(ctx context.Context, _ *struct{}) (*usersOut, error) {
	rows, err := r.q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	out := &usersOut{Body: make([]UserOut, 0, len(rows))}
	for _, u := range rows {
		out.Body = append(out.Body, userOut(u))
	}
	return out, nil
}

type updateMeIn struct{ Body UserProfileUpdate }

// updateMe locks the caller's row, so a clash is judged against the email
// they hold now. A clash that slips past the check (two writers) is the
// unique key's 409.
func (r routes) updateMe(ctx context.Context, in *updateMeIn) (*userBody, error) {
	b := in.Body
	var out authdb.User
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		cur, err := q.UserByIDForUpdate(ctx, platform.PrincipalFrom(ctx).UserID)
		if err != nil {
			return err
		}
		addr, name := cur.Email, cur.DisplayName
		if b.Email != nil && string(*b.Email) != cur.Email {
			clash, err := q.UserByEmail(ctx, string(*b.Email))
			if err == nil && clash.UserID != cur.UserID {
				return platform.Err(http.StatusConflict, "email_in_use", emailInUse)
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			addr = string(*b.Email)
		}
		if b.DisplayName != nil {
			name = b.DisplayName
			if *name == "" {
				name = nil
			}
		}
		out, err = q.UpdateProfile(ctx, authdb.UpdateProfileParams{UserID: cur.UserID, Email: addr, DisplayName: name})
		return platform.MapConstraint(err, emailConstraint)
	})
	if err != nil {
		return nil, err
	}
	return &userBody{Body: userOut(out)}, nil
}

type createUserIn struct{ Body UserCreate }

func (r routes) createUser(ctx context.Context, in *createUserIn) (*userBody, error) {
	b := in.Body
	_, err := r.q.UserByEmail(ctx, string(b.Email))
	if err == nil {
		return nil, platform.Err(http.StatusConflict, "email_already_exists", emailExists)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	hash, err := hashPassword(ctx, b.Password)
	if err != nil {
		return nil, err
	}
	role := b.Role
	if role == "" {
		role = "admin"
	}
	u, err := r.q.CreateUser(ctx, authdb.CreateUserParams{
		UserID: uuid.New(), Email: string(b.Email), PasswordHash: hash, Role: role, DisplayName: b.DisplayName,
	})
	if err != nil {
		return nil, platform.MapConstraint(err, emailExistsConstraint)
	}
	return &userBody{Body: userOut(u)}, nil
}

type adminResetIn struct {
	UserID uuid.UUID `path:"user_id"`
	Body   UserPasswordReset
}

// adminResetPassword is the set-password command for one user: the new hash,
// a moved session epoch (every session of theirs ends) and no reset token.
func (r routes) adminResetPassword(ctx context.Context, in *adminResetIn) (*userBody, error) {
	if _, err := r.q.UserByID(ctx, in.UserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, platform.Err(http.StatusNotFound, "user_not_found", userNotFound)
		}
		return nil, err
	}
	hash, err := hashPassword(ctx, in.Body.Password)
	if err != nil {
		return nil, err
	}
	u, err := r.q.SetPassword(ctx, authdb.SetPasswordParams{UserID: in.UserID, PasswordHash: hash})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Err(http.StatusNotFound, "user_not_found", userNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &userBody{Body: userOut(u)}, nil
}
