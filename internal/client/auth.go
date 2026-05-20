// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

// Authorizer attaches credentials to outgoing Listmonk API requests.
//
// Listmonk's `auth.Middleware` (see knadh/listmonk cmd/auth.go) accepts
// two interchangeable credential carriers for any non-cookie API call:
//
//   - HTTP Basic-Auth: `Authorization: Basic <base64(user:token)>`
//   - Custom header:   `Authorization: token <user>:<token>`
//
// For a user with `type=api`, the user's password IS the token — there is
// no separate token-mint endpoint. We default to Basic-Auth (RFC 7617);
// both forms hit the same code path on the server.
type Authorizer interface {
	Authorize(ctx context.Context, req *http.Request) error
}

// BasicAuth attaches `Authorization: Basic <base64(username:token)>`.
// `Token` is the password of a `type=api` Listmonk User; for human users
// it is the raw account password (but management endpoints should be
// driven by an api-typed user — see the provider README).
type BasicAuth struct {
	Username string
	Token    string
}

func (a *BasicAuth) Authorize(_ context.Context, req *http.Request) error {
	req.SetBasicAuth(a.Username, a.Token)
	return nil
}
