package server

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// People sign in with a username and password and get a session cookie.
// The iOS Shortcut and scripts use a personal API token as a bearer token.
// DOCVAULT_TOKEN isn't a user: it creates the first account and is the
// key for Foyer's widget and Drop uploads.

const (
	cookieName  = "docvault_session"
	sessionIdle = 180 * 24 * time.Hour
	tokenPrefix = "dv_"
)

func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// secret is a random value for a session or an API token.
func secret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashSecret is how sessions and API tokens are stored: they're random,
// so a plain SHA-256 is enough.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pbkdf2Iterations is OWASP's 2023 advice for PBKDF2-SHA256 (tests lower it).
var pbkdf2Iterations = 600_000

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func checkPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err != nil || err1 != nil || err2 != nil || iter < 1 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// A dummy hash, so a wrong username takes as long as a wrong password.
var dummyHash, _ = hashPassword("docvault")

type ctxKey int

const (
	userKey ctxKey = iota
	viaTokenKey
)

func withUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

// identify finds who's calling: a session cookie or a personal API token.
// viaToken is set for API tokens.
func (s *Server) identify(r *http.Request) (u *store.User, viaToken bool) {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		bearer = strings.TrimSpace(bearer)
		if !strings.HasPrefix(bearer, tokenPrefix) {
			return nil, false
		}
		u, err := s.Store.TokenUser(r.Context(), hashSecret(bearer))
		if err != nil {
			return nil, false
		}
		return u, true
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		u, err := s.Store.SessionUser(r.Context(), hashSecret(c.Value), sessionIdle)
		if err == nil {
			return u, false
		}
	}
	return nil, false
}

func (s *Server) isSystem(r *http.Request) bool {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && equal(strings.TrimSpace(bearer), s.Token)
}

// requireUser lets signed-in people and API tokens through.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, viaToken := s.identify(r)
		if u == nil {
			msg := "sign in to Docvault"
			if s.isSystem(r) {
				msg = "DOCVAULT_TOKEN only works for Foyer; use a personal API token from Settings"
			} else if r.Header.Get("Authorization") != "" {
				msg = "unknown API token; make a new one under Settings → iPhone"
			}
			writeError(w, http.StatusUnauthorized, msg)
			return
		}
		ctx := context.WithValue(withUser(r.Context(), u), viaTokenKey, viaToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionOnly keeps API tokens away from account settings: a token on a
// phone can upload, but can't make more tokens or change passwords.
func sessionOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if via, _ := r.Context().Value(viaTokenKey).(bool); via {
			writeError(w, http.StatusForbidden, "API tokens can't change account settings")
			return
		}
		next(w, r)
	}
}

func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return sessionOnly(func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).Admin {
			writeError(w, http.StatusForbidden, "only an admin can do that")
			return
		}
		next(w, r)
	})
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
// curl) rely on the token they carry.
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
	// SetupNeeded: no accounts yet, so the first one is made with the token.
	SetupNeeded bool        `json:"setup_needed,omitempty"`
	User        *store.User `json:"user,omitempty"`
	// FoyerURL is the homelab's start page, linked from the header.
	FoyerURL string `json:"foyer_url,omitempty"`
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	info := sessionInfo{FoyerURL: s.FoyerURL}
	if u, viaToken := s.identify(r); u != nil && !viaToken {
		info.Authenticated, info.User = true, u
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
	writeJSON(w, http.StatusOK, sessionInfo{Authenticated: true, User: u, FoyerURL: s.FoyerURL})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	u, err := s.Store.UserByName(r.Context(), strings.TrimSpace(body.Username))
	hash := dummyHash
	if err == nil {
		hash = u.Password()
	} else if !errors.Is(err, store.ErrNotFound) {
		storeError(w, err)
		return
	}
	if !checkPassword(hash, body.Password) || u == nil {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		writeError(w, http.StatusUnauthorized, "wrong username or password")
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
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

func (b *accountBody) clean(needPassword bool) string {
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
	case (needPassword || b.Password != "") && len(b.Password) < 8:
		return "the password needs at least 8 characters"
	case len(b.Password) > 200:
		return "the password is too long"
	}
	return ""
}

// setup creates the first account (an admin) with DOCVAULT_TOKEN as proof.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		accountBody
		Token string `json:"token"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	if !equal(strings.TrimSpace(body.Token), s.Token) {
		time.Sleep(500 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "wrong token: use the DOCVAULT_TOKEN from the server's settings")
		return
	}
	if n, err := s.Store.CountUsers(r.Context()); err != nil || n > 0 {
		writeError(w, http.StatusConflict, "Docvault is already set up; sign in instead")
		return
	}
	if msg := body.clean(true); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		storeError(w, err)
		return
	}
	u := &store.User{Username: body.Username, Name: body.Name, Admin: true}
	if err := s.Store.CreateUser(r.Context(), u, hash); err != nil {
		storeError(w, err)
		return
	}
	s.startSession(w, r, u)
}

// updateMe changes your own name and password.
func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)
	var body struct {
		Name            string `json:"name"`
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	acc := accountBody{Username: me.Username, Name: body.Name, Password: body.Password}
	if msg := acc.clean(false); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	hash := ""
	if body.Password != "" {
		if !checkPassword(me.Password(), body.CurrentPassword) {
			time.Sleep(500 * time.Millisecond)
			writeError(w, http.StatusForbidden, "the current password is wrong")
			return
		}
		var err error
		if hash, err = hashPassword(body.Password); err != nil {
			storeError(w, err)
			return
		}
	}
	u := *me
	u.Name = acc.Name
	if err := s.Store.UpdateUser(r.Context(), &u, hash); err != nil {
		storeError(w, err)
		return
	}
	if hash != "" {
		s.startSession(w, r, &u) // the change signed every browser out
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
	creating := r.PathValue("id") == ""
	if msg := body.clean(creating); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	hash := ""
	if body.Password != "" {
		var err error
		if hash, err = hashPassword(body.Password); err != nil {
			storeError(w, err)
			return
		}
	}
	if creating {
		u := &store.User{Username: body.Username, Name: body.Name, Admin: body.Admin}
		if err := s.Store.CreateUser(r.Context(), u, hash); err != nil {
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
	if err := s.Store.UpdateUser(r.Context(), u, hash); err != nil {
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

// ── API tokens ──────────────────────────────────────────────────────────

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.APITokens(r.Context(), currentUser(r).ID)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, 4<<10, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 60 {
		writeError(w, http.StatusBadRequest, "give the token a name, like “Dad's iPhone”")
		return
	}
	value := tokenPrefix + secret()
	t := store.APIToken{Name: body.Name, Hint: value[len(value)-4:]}
	if err := s.Store.CreateAPIToken(r.Context(), currentUser(r).ID, &t, hashSecret(value)); err != nil {
		storeError(w, err)
		return
	}
	// The only time the token is shown.
	writeJSON(w, http.StatusCreated, struct {
		store.APIToken
		Token string `json:"token"`
	}{t, value})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteAPIToken(r.Context(), currentUser(r).ID, id); err != nil {
		storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
