package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// Docvault is a family app reached only over Tailscale, so the username is
// the whole sign-in: no passwords, no sign-up. An admin adds the accounts.
// The browser gets a session cookie; the iOS Shortcut and scripts send the
// username in the X-Docvault-User header. DOCVAULT_TOKEN isn't a user: it
// is the key for Foyer's widget and Drop uploads.

const (
	cookieName  = "docvault_session"
	sessionIdle = 180 * 24 * time.Hour
	userHeader  = "X-Docvault-User"
)

func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// secret is a random session value.
func secret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashSecret is how sessions are stored: they're random, so a plain
// SHA-256 is enough.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type ctxKey int

const userKey ctxKey = 0

func withUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

// identify finds who's calling: the X-Docvault-User header, or a session
// cookie. Browsers can't send that header from another site without a
// CORS preflight, which Docvault never allows.
func (s *Server) identify(r *http.Request) *store.User {
	if name := strings.TrimSpace(r.Header.Get(userHeader)); name != "" {
		u, err := s.Store.UserByName(r.Context(), name)
		if err != nil {
			return nil
		}
		return u
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		u, err := s.Store.SessionUser(r.Context(), hashSecret(c.Value), sessionIdle)
		if err == nil {
			return u
		}
	}
	return nil
}

func (s *Server) isSystem(r *http.Request) bool {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.Token != "" && equal(strings.TrimSpace(bearer), s.Token)
}

// requireUser lets signed-in people (and the Shortcut) through.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := s.identify(r)
		if u == nil {
			msg := "sign in to Docvault"
			if r.Header.Get(userHeader) != "" {
				msg = "no such user: check the username in the Shortcut"
			} else if s.isSystem(r) {
				msg = "DOCVAULT_TOKEN only works for Foyer; send your username in the " + userHeader + " header"
			}
			writeError(w, http.StatusUnauthorized, msg)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).Admin {
			writeError(w, http.StatusForbidden, "only an admin can do that")
			return
		}
		next(w, r)
	}
}

// requireSystem is for Foyer: the DOCVAULT_TOKEN as a bearer token.
func (s *Server) requireSystem(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isSystem(r) {
			writeError(w, http.StatusUnauthorized, "needs DOCVAULT_TOKEN as a bearer token")
			return
		}
		next(w, r)
	}
}

// sameOrigin refuses state-changing requests that a browser sent from
// another origin. Requests without these headers (the Shortcut, Foyer,
// curl) come from outside a browser.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			site := r.Header.Get("Sec-Fetch-Site")
			if site != "" && site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					writeError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type sessionInfo struct {
	Authenticated bool `json:"authenticated"`
	// SetupNeeded: no accounts yet, so the first one (an admin) is made
	// from the sign-in page.
	SetupNeeded bool        `json:"setup_needed,omitempty"`
	User        *store.User `json:"user,omitempty"`
	// FoyerURL is the homelab's start page, linked from the header. Only
	// admins get it: the family uses Docvault, not the rest of the homelab.
	FoyerURL string `json:"foyer_url,omitempty"`
}

func (s *Server) signedIn(u *store.User) sessionInfo {
	info := sessionInfo{Authenticated: true, User: u}
	if u.Admin {
		info.FoyerURL = s.FoyerURL
	}
	return info
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	info := sessionInfo{}
	if u := s.identify(r); u != nil {
		info = s.signedIn(u)
	} else if n, err := s.Store.CountUsers(r.Context()); err == nil && n == 0 {
		info.SetupNeeded = true
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	value := secret()
	if err := s.Store.CreateSession(r.Context(), hashSecret(value), u.ID); err != nil {
		storeError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: value, Path: "/",
		MaxAge: int(sessionIdle / time.Second), HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, http.StatusOK, s.signedIn(u))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	u, err := s.Store.UserByName(r.Context(), strings.TrimSpace(body.Username))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "no such user: ask whoever runs Docvault to add you")
		return
	}
	if err != nil {
		storeError(w, err)
		return
	}
	s.startSession(w, r, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		_ = s.Store.DeleteSession(r.Context(), hashSecret(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}

type accountBody struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Admin    bool   `json:"admin"`
}

func (b *accountBody) clean() string {
	b.Username = strings.ToLower(strings.TrimSpace(b.Username))
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		b.Name = b.Username
	}
	switch {
	case b.Username == "" || len(b.Username) > 40 || strings.ContainsAny(b.Username, " /\\:@"):
		return "a username is required (no spaces, up to 40 characters)"
	case len(b.Name) > 80:
		return "the name is too long"
	}
	return ""
}

// setup creates the first account, an admin, on a fresh install.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body accountBody
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	if n, err := s.Store.CountUsers(r.Context()); err != nil || n > 0 {
		writeError(w, http.StatusConflict, "Docvault is already set up; sign in instead")
		return
	}
	if msg := body.clean(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	u := &store.User{Username: body.Username, Name: body.Name, Admin: true}
	if err := s.Store.CreateUser(r.Context(), u); err != nil {
		storeError(w, err)
		return
	}
	s.startSession(w, r, u)
}

// updateMe changes your own name.
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	acc := accountBody{Username: me.Username, Name: body.Name}
	if msg := acc.clean(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	u := *me
	u.Name = acc.Name
	if err := s.Store.UpdateUser(r.Context(), &u); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// ── People (admin) ──────────────────────────────────────────────────────

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Store.Users(r.Context())
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) saveUser(w http.ResponseWriter, r *http.Request) {
	var body accountBody
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	if msg := body.clean(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if r.PathValue("id") == "" {
		u := &store.User{Username: body.Username, Name: body.Name, Admin: body.Admin}
		if err := s.Store.CreateUser(r.Context(), u); err != nil {
			if errors.Is(err, store.ErrConflict) {
				writeError(w, http.StatusConflict, "that username is taken")
				return
			}
			storeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, u)
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := s.Store.User(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	if u.ID == currentUser(r).ID && !body.Admin {
		writeError(w, http.StatusBadRequest, "you can't remove your own admin rights")
		return
	}
	u.Name, u.Admin = body.Name, body.Admin
	if err := s.Store.UpdateUser(r.Context(), u); err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == currentUser(r).ID {
		writeError(w, http.StatusBadRequest, "you can't delete your own account")
		return
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrInUse) {
			writeError(w, http.StatusConflict, "they still have private documents: move them to Family or delete them first")
			return
		}
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
