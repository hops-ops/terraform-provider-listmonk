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
