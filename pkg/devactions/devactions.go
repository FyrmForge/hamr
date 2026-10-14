// Package devactions exposes app-defined dev/staging actions (wipe, reseed,
// create account, ...) and test accounts to hamr over one token-gated
// endpoint. See docs/proposed-features/staging-proxy-and-dev-actions.md.
package devactions

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/FyrmForge/hamr/pkg/auth"
)

// Prefix is where the app mounts Handler.
const Prefix = "/_devactions"

// InputType is the kind of form field hamr renders for an Input.
type InputType string

const (
	InputText   InputType = "text"
	InputNumber InputType = "number"
	InputBool   InputType = "bool"
	InputSelect InputType = "select"
)

// Input describes one action parameter.
type Input struct {
	Name     string
	Label    string
	Type     InputType
	Options  []string // InputSelect only
	Default  string
	Required bool
}

// Action is one button in the hamr widget.
type Action struct {
	Name        string
	Label       string
	Description string
	Inputs      []Input
	Dangerous   bool
	Run         func(ctx context.Context, in map[string]string) (message string, err error)
}

// Account is a seeded test login offered by the widget. Phone is optional;
// when set, the widget links the user's texts in the SMS inbox.
type Account struct {
	Email    string
	Password string
	Label    string
	Phone    string
}

// Registry holds the app's actions.
type Registry struct {
	Accounts   func(ctx context.Context) ([]Account, error)
	Disposable func(ctx context.Context) (bool, error)
	// WhoAmI resolves the browser session on r (hamr forwards the browser's
	// cookies) to the logged-in user, or nil when nobody is. Optional; it
	// powers the widget's "<user>'s mail / texts" links. SessionWhoAmI builds
	// one from a pkg/auth SessionManager. Password is never sent.
	WhoAmI func(r *http.Request) (*Account, error)

	actions []Action
}

// Register adds an action. It panics on a duplicate name or one that is not a
// single URL path segment: only [A-Za-z0-9._-], not "." or "..".
func (r *Registry) Register(a Action) {
	if a.Name == "" {
		panic("devactions: action name is empty")
	}
	if a.Name == "." || a.Name == ".." || strings.Trim(a.Name, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._-") != "" {
		panic("devactions: action name " + a.Name + " must be a single path segment [A-Za-z0-9._-]")
	}
	if a.Name == whoamiName {
		panic("devactions: action name " + whoamiName + " is reserved")
	}
	for _, e := range r.actions {
		if e.Name == a.Name {
			panic("devactions: duplicate action " + a.Name)
		}
	}
	r.actions = append(r.actions, a)
}

// Handler returns the endpoint, or nil when the env gate fails:
// HAMR_ACTIONS_TOKEN must be set and HAMR_ACTIONS_ENV must be "development"
// or "staging". It panics if a Dangerous action exists without Disposable.
// The handler matches on the full request path, so mount it at the app root.
func (r *Registry) Handler() http.Handler {
	token := os.Getenv("HAMR_ACTIONS_TOKEN")
	env := os.Getenv("HAMR_ACTIONS_ENV")
	if token == "" || (env != "development" && env != "staging") {
		return nil
	}
	for _, a := range r.actions {
		if a.Dangerous && r.Disposable == nil {
			panic("devactions: dangerous action " + a.Name + " requires Registry.Disposable")
		}
	}
	h := &handler{r: r, actions: slices.Clone(r.actions), token: sha256.Sum256([]byte(token))}
	return h
}

type handler struct {
	r       *Registry
	actions []Action
	token   [32]byte
}

const maxBody = 1 << 20

// whoamiName is the reserved path segment for the WhoAmI route.
const whoamiName = "whoami"

type inputJSON struct {
	Name     string    `json:"name"`
	Label    string    `json:"label"`
	Type     InputType `json:"type"`
	Options  []string  `json:"options,omitempty"`
	Default  string    `json:"default"`
	Required bool      `json:"required"`
}

type actionJSON struct {
	Name        string      `json:"name"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Dangerous   bool        `json:"dangerous"`
	Inputs      []inputJSON `json:"inputs"`
}

type accountJSON struct {
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
	Label    string `json:"label"`
	Phone    string `json:"phone,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p := req.URL.Path
	if p != Prefix && !strings.HasPrefix(p, Prefix+"/") {
		http.NotFound(w, req)
		return
	}
	got := sha256.Sum256([]byte(req.Header.Get("X-Hamr-Actions-Token")))
	if subtle.ConstantTimeCompare(got[:], h.token[:]) != 1 {
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if p == Prefix {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.list(w, req)
		return
	}
	name := strings.TrimPrefix(p, Prefix+"/")
	if name == whoamiName {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.whoami(w, req)
		return
	}
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h.run(w, req, name)
}

func (h *handler) list(w http.ResponseWriter, req *http.Request) {
	actions := make([]actionJSON, 0, len(h.actions))
	for _, a := range h.actions {
		aj := actionJSON{Name: a.Name, Label: a.Label, Description: a.Description, Dangerous: a.Dangerous, Inputs: []inputJSON{}}
		for _, in := range a.Inputs {
			ij := inputJSON{Name: in.Name, Label: in.Label, Type: in.Type, Default: in.Default, Required: in.Required}
			if in.Type == InputSelect {
				ij.Options = in.Options
			}
			aj.Inputs = append(aj.Inputs, ij)
		}
		actions = append(actions, aj)
	}
	accounts := []accountJSON{}
	if h.r.Accounts != nil {
		accs, err := h.r.Accounts(req.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, a := range accs {
			accounts = append(accounts, accountJSON(a))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions, "accounts": accounts})
}

// whoami answers {"account": {...}} or {"account": null}. 404 when the app
// registered no WhoAmI, so the widget can tell "nobody" from "unsupported".
func (h *handler) whoami(w http.ResponseWriter, req *http.Request) {
	if h.r.WhoAmI == nil {
		writeErr(w, http.StatusNotFound, "whoami not supported")
		return
	}
	a, err := h.r.WhoAmI(req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out *accountJSON
	if a != nil {
		out = &accountJSON{Email: a.Email, Label: a.Label, Phone: a.Phone}
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": out})
}

func (h *handler) run(w http.ResponseWriter, req *http.Request, name string) {
	idx := slices.IndexFunc(h.actions, func(a Action) bool { return a.Name == name })
	if idx < 0 {
		writeErr(w, http.StatusNotFound, "unknown action")
		return
	}
	a := h.actions[idx]

	var body struct {
		Inputs  map[string]string `json:"inputs"`
		Confirm bool              `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxBody)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	in := make(map[string]string, len(a.Inputs))
	for _, def := range a.Inputs {
		v, ok := body.Inputs[def.Name]
		if !ok || v == "" {
			v = def.Default
		}
		if err := validate(def, v); err != "" {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		in[def.Name] = v
	}

	if a.Dangerous {
		if !body.Confirm {
			writeErr(w, http.StatusConflict, "confirmation required")
			return
		}
		ok, err := h.r.Disposable(req.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeErr(w, http.StatusPreconditionFailed, "database is not marked disposable")
			return
		}
	}

	msg, err := a.Run(req.Context(), in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// validate returns a user-facing error, or "" when v is acceptable.
func validate(def Input, v string) string {
	if v == "" {
		if def.Required {
			return def.Name + " is required"
		}
		return ""
	}
	switch def.Type {
	case InputSelect:
		if !slices.Contains(def.Options, v) {
			return def.Name + " must be one of the listed options"
		}
	case InputNumber:
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return def.Name + " must be a number"
		}
	case InputBool:
		if v != "true" && v != "false" {
			return def.Name + " must be true or false"
		}
	}
	return ""
}

// SessionWhoAmI builds a Registry.WhoAmI from a pkg/auth session manager:
// the session cookie is validated, then lookup turns the session's subject
// ID into the account shown in the widget (nil = not a known user).
func SessionWhoAmI(sm *auth.SessionManager, lookup func(ctx context.Context, subjectID string) (*Account, error)) func(*http.Request) (*Account, error) {
	return func(r *http.Request) (*Account, error) {
		c, err := r.Cookie(sm.CookieName())
		if err != nil || c.Value == "" {
			return nil, nil
		}
		s, err := sm.ValidateSession(r.Context(), c.Value)
		if err != nil || s == nil {
			return nil, err
		}
		return lookup(r.Context(), s.SubjectID)
	}
}
