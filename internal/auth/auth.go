package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

type Authenticator interface {
	Enabled() bool
	Authorize(r *http.Request) bool
}

type noop struct{}

func Noop() Authenticator {
	return noop{}
}

func (noop) Enabled() bool {
	return false
}

func (noop) Authorize(*http.Request) bool {
	return true
}

type tokenAuth struct {
	token string
}

func NewToken(token string) Authenticator {
	return tokenAuth{token: token}
}

func (a tokenAuth) Enabled() bool {
	return strings.TrimSpace(a.token) != ""
}

func (a tokenAuth) Authorize(r *http.Request) bool {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Fluid-Token"))
	}
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) == 1
}

type basicAuth struct {
	user string
	pass string
}

func NewBasic(user, pass string) Authenticator {
	return basicAuth{user: user, pass: pass}
}

func (a basicAuth) Enabled() bool {
	return strings.TrimSpace(a.user) != "" || strings.TrimSpace(a.pass) != ""
}

func (a basicAuth) Authorize(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Basic ") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(parts[0]), []byte(a.user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(parts[1]), []byte(a.pass)) == 1
	return userOK && passOK
}
