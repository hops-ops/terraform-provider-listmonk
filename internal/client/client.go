// SPDX-License-Identifier: MPL-2.0

// Package client is a thin Go HTTP client for Listmonk's REST API
// (https://listmonk.app/docs/apis/apis/). It exposes only the surface
// the Terraform provider needs — see knadh/listmonk cmd/handlers.go for
// the full route table.
//
// Authentication is delegated to an Authorizer (see auth.go). Listmonk
// accepts HTTP Basic-Auth on every API endpoint and that is currently
// the only mode this client supports.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
	apiPrefix      = "/api"
)

// Client is the Listmonk REST client.
type Client struct {
	endpoint  string
	auth      Authorizer
	userAgent string
	http      *http.Client
}

// New constructs a Client. endpoint is the base URL of the Listmonk
// instance (e.g. http://marketing-listmonk.marketing.svc.cluster.local:9000);
// the client appends /api/* to each request.
func New(endpoint string, auth Authorizer, providerVersion string) *Client {
	return &Client{
		endpoint:  strings.TrimRight(endpoint, "/"),
		auth:      auth,
		userAgent: fmt.Sprintf("terraform-provider-listmonk/%s", providerVersion),
		http: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// APIError surfaces a non-2xx response with the body included so callers
// can write useful diagnostics.
type APIError struct {
	StatusCode int
	Body       string
	Method     string
	Path       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("listmonk: %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is a 404 from the Listmonk API. Used in
// resource Read methods to clear state when a resource was deleted
// out-of-band.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	ae, ok := err.(*APIError)
	return ok && ae.StatusCode == http.StatusNotFound
}

// do executes an HTTP request against /api/<path>, marshalling body as
// JSON (if non-nil) and decoding the response into out (if non-nil).
//
// Listmonk wraps successful responses in `{"data": ...}`; this helper
// unwraps the envelope before delivering to `out`. Pre-marshalled JSON
// (json.RawMessage) is sent verbatim — used by the per-key settings
// PUT, which expects the value JSON value directly.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	url := c.endpoint + apiPrefix + path

	var reqBody io.Reader
	if body != nil {
		// json.RawMessage is already encoded — send it through unchanged.
		if raw, ok := body.(json.RawMessage); ok {
			reqBody = bytes.NewReader(raw)
		} else {
			buf, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal request body: %w", err)
			}
			reqBody = bytes.NewReader(buf)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if err := c.auth.Authorize(ctx, req); err != nil {
		return fmt.Errorf("authorize request: %w", err)
	}
	req.Header.Set("user-agent", c.userAgent)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Method:     method,
			Path:       apiPrefix + path,
		}
	}

	if out != nil && len(respBody) > 0 {
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(respBody, &envelope); err == nil && len(envelope.Data) > 0 {
			if err := json.Unmarshal(envelope.Data, out); err != nil {
				return fmt.Errorf("decode response body (data envelope): %w", err)
			}
		} else if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response body: %w", err)
		}
	}

	return nil
}

// ---------------------------------------------------------------------
// Settings (the `settings` table — one row per dotted key, JSONB value)
//
// Schema (knadh/listmonk schema.sql):
//
//	CREATE TABLE settings (
//	    key   TEXT NOT NULL UNIQUE,
//	    value JSONB NOT NULL DEFAULT '{}',
//	    ...
//	);
//
// GetSettings reads via JSON_OBJECT_AGG so the response is a single
// JSON object keyed by the dotted setting names (e.g. "security.oidc",
// "app.root_url"). The value of each key is its raw JSONB.
//
// UpdateSettingByKey targets the row identified by `:key` — it does NOT
// upsert (the migration seeds every standard key with a default), so
// passing an unknown key is a no-op rather than an error from Listmonk's
// perspective. Each TF resource is responsible for picking a key that
// exists in the live schema.
//
// Per-key value typing lives in the calling resource (e.g.
// SecuritySettingsResource owns the OIDC sub-block schema) — this client
// stays untyped at the settings layer.
// ---------------------------------------------------------------------

// SettingsBag is the response shape of GET /api/settings: dotted key →
// raw JSON value.
type SettingsBag map[string]json.RawMessage

// GetSettings fetches the full settings tree from /api/settings.
// `client_secret` / SMTP / OIDC password fields come back masked
// (bullet-character runs) per the upstream handler — callers must keep
// secret values authoritative in Terraform state, not detect drift on
// them from the API response.
func (c *Client) GetSettings(ctx context.Context) (SettingsBag, error) {
	out := SettingsBag{}
	if err := c.do(ctx, http.MethodGet, "/settings", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSettingByKey writes a single dotted-key row in the settings
// table via PUT /api/settings/:key. The body is the raw JSON value for
// that key (the Listmonk handler binds the body to a json.RawMessage
// and stores it directly).
func (c *Client) UpdateSettingByKey(ctx context.Context, key string, value json.RawMessage) error {
	// PUT body is the JSON value itself — not wrapped. Listmonk's
	// handlers.go binds the body to a json.RawMessage and passes it
	// straight through to UPDATE settings SET value = $2 ...
	return c.do(ctx, http.MethodPut, "/settings/"+key, value, nil)
}

// ---------------------------------------------------------------------
// User roles (the `roles` table, filtered by type='user')
//
// Upstream routes (cmd/handlers.go):
//   GET    /api/roles/users      — list all user roles
//   POST   /api/roles/users      — create
//   PUT    /api/roles/users/:id  — update
//   DELETE /api/roles/:id        — delete (singular: serves both user + list role deletes)
//
// There is no per-ID GET; we list + filter client-side for the Read path.
// ---------------------------------------------------------------------

// UserRole is the "user"-typed roles row shape from upstream
// `models.Role`. Permissions are dotted-string scoped capabilities like
// `settings:manage`, `users:get`, `lists:manage_all`.
type UserRole struct {
	ID          int64    `json:"id,omitempty"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

func (c *Client) CreateUserRole(ctx context.Context, r *UserRole) (*UserRole, error) {
	var out UserRole
	if err := c.do(ctx, http.MethodPost, "/roles/users", r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUserRole lists all user roles and returns the one matching id.
// Upstream has no per-ID GET — the cost is one full-list call per Read
// pass; acceptable because the user-roles table is small (single-digit
// rows in typical installs).
func (c *Client) GetUserRole(ctx context.Context, id int64) (*UserRole, error) {
	var all []UserRole
	if err := c.do(ctx, http.MethodGet, "/roles/users", nil, &all); err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, &APIError{StatusCode: http.StatusNotFound, Method: http.MethodGet, Path: apiPrefix + "/roles/users", Body: fmt.Sprintf("no role with id=%d", id)}
}

func (c *Client) UpdateUserRole(ctx context.Context, id int64, r *UserRole) (*UserRole, error) {
	var out UserRole
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf("/roles/users/%d", id), r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteUserRole(ctx context.Context, id int64) error {
	// DELETE /api/roles/:id (no `/users` segment — the same endpoint
	// serves both user roles and list roles).
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/roles/%d", id), nil, nil)
}

// ---------------------------------------------------------------------
// Users (the `users` table)
//
// Upstream routes (cmd/handlers.go):
//   GET    /api/users        — list all
//   GET    /api/users/:id    — get one
//   POST   /api/users        — create
//   PUT    /api/users/:id    — update
//   DELETE /api/users/:id    — delete
//
// For `type=api` users, the user's password IS the token used for
// HTTP Basic-Auth — the API returns it in plaintext on Create. The
// apiUsers cache only refreshes at pod startup (see
// reference_listmonk_apiusers_cache_at_startup); newly-created api
// users are NOT immediately usable for Basic-Auth without a restart.
// ---------------------------------------------------------------------

// UserType is "user" (interactive admin, password_login true) or
// "api" (machine credential, password = plaintext token).
type UserType string

const (
	UserTypeUser UserType = "user"
	UserTypeAPI  UserType = "api"
)

// UserStatus is "enabled" or "disabled". Disabled users still exist
// (history is preserved) but can't authenticate.
type UserStatus string

const (
	UserStatusEnabled  UserStatus = "enabled"
	UserStatusDisabled UserStatus = "disabled"
)

// User mirrors upstream `models.User`. For type=api: PasswordLogin
// is false; Password is the plaintext token returned at Create time
// (and accepted at Update time when rotating). For type=user:
// PasswordLogin is typically true; Password is the operator-chosen
// password (stored bcrypt-hashed server-side; not returned on read).
//
// Asymmetric wire shape: POST/PUT requests accept flat
// `user_role_id` + `list_role_id` integer fields, but GET responses
// return nested `user_role: {id, name, permissions, ...}` + `list_role`
// objects. Custom MarshalJSON / UnmarshalJSON bridges both — the Go
// struct stays flat (UserRoleID + ListRoleID); marshal writes the
// flat shape Listmonk expects on writes; unmarshal accepts either the
// flat or nested forms and normalizes to the flat IDs.
type User struct {
	ID            int64      `json:"-"`
	Username      string     `json:"-"`
	Email         string     `json:"-"`
	Name          string     `json:"-"`
	Type          UserType   `json:"-"`
	UserRoleID    int64      `json:"-"`
	ListRoleID    *int64     `json:"-"`
	Status        UserStatus `json:"-"`
	PasswordLogin bool       `json:"-"`
	Password      string     `json:"-"`
	CreatedAt     string     `json:"-"`
	UpdatedAt     string     `json:"-"`
}

// MarshalJSON emits the flat `user_role_id` / `list_role_id` shape
// Listmonk's POST/PUT handlers accept.
func (u User) MarshalJSON() ([]byte, error) {
	wire := struct {
		ID            int64      `json:"id,omitempty"`
		Username      string     `json:"username"`
		Email         string     `json:"email"`
		Name          string     `json:"name"`
		Type          UserType   `json:"type"`
		UserRoleID    int64      `json:"user_role_id"`
		ListRoleID    *int64     `json:"list_role_id,omitempty"`
		Status        UserStatus `json:"status"`
		PasswordLogin bool       `json:"password_login"`
		Password      string     `json:"password,omitempty"`
		CreatedAt     string     `json:"created_at,omitempty"`
		UpdatedAt     string     `json:"updated_at,omitempty"`
	}{
		ID:            u.ID,
		Username:      u.Username,
		Email:         u.Email,
		Name:          u.Name,
		Type:          u.Type,
		UserRoleID:    u.UserRoleID,
		ListRoleID:    u.ListRoleID,
		Status:        u.Status,
		PasswordLogin: u.PasswordLogin,
		Password:      u.Password,
		CreatedAt:     u.CreatedAt,
		UpdatedAt:     u.UpdatedAt,
	}
	return json.Marshal(wire)
}

// UnmarshalJSON accepts Listmonk's GET response shape — nested
// `user_role` / `list_role` objects — and flattens to UserRoleID +
// ListRoleID. Also tolerates a flat shape (in case upstream changes
// or we're round-tripping our own output).
func (u *User) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID            int64      `json:"id,omitempty"`
		Username      string     `json:"username"`
		Email         string     `json:"email"`
		Name          string     `json:"name"`
		Type          UserType   `json:"type"`
		Status        UserStatus `json:"status"`
		PasswordLogin bool       `json:"password_login"`
		Password      string     `json:"password,omitempty"`
		CreatedAt     string     `json:"created_at,omitempty"`
		UpdatedAt     string     `json:"updated_at,omitempty"`
		// Nested objects (Listmonk's GET shape)
		UserRole *struct {
			ID int64 `json:"id"`
		} `json:"user_role,omitempty"`
		ListRole *struct {
			ID int64 `json:"id"`
		} `json:"list_role,omitempty"`
		// Flat fields (Listmonk's POST/PUT request shape; also accepted
		// in case upstream ever flattens the response)
		UserRoleIDFlat int64  `json:"user_role_id,omitempty"`
		ListRoleIDFlat *int64 `json:"list_role_id,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	u.ID = wire.ID
	u.Username = wire.Username
	u.Email = wire.Email
	u.Name = wire.Name
	u.Type = wire.Type
	u.Status = wire.Status
	u.PasswordLogin = wire.PasswordLogin
	u.Password = wire.Password
	u.CreatedAt = wire.CreatedAt
	u.UpdatedAt = wire.UpdatedAt

	switch {
	case wire.UserRole != nil:
		u.UserRoleID = wire.UserRole.ID
	default:
		u.UserRoleID = wire.UserRoleIDFlat
	}
	switch {
	case wire.ListRole != nil:
		id := wire.ListRole.ID
		u.ListRoleID = &id
	default:
		u.ListRoleID = wire.ListRoleIDFlat
	}
	return nil
}

func (c *Client) CreateUser(ctx context.Context, u *User) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodPost, "/users", u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetUser(ctx context.Context, id int64) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/users/%d", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateUser(ctx context.Context, id int64, u *User) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf("/users/%d", id), u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteUser(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/users/%d", id), nil, nil)
}
