package devactions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyrmForge/hamr/pkg/auth"
)

// memSessions is a minimal auth.SessionStore keyed by token.
type memSessions map[string]*auth.Session

func (m memSessions) Create(_ context.Context, s *auth.Session) error { m[s.Token] = s; return nil }
func (m memSessions) GetByToken(_ context.Context, t string) (*auth.Session, error) {
	return m[t], nil
}
func (m memSessions) Delete(context.Context, string) error            { return nil }
func (m memSessions) DeleteBySubjectID(context.Context, string) error { return nil }

func TestWhoAmI(t *testing.T) {
	setEnv(t, "tok", "staging")
	sm := auth.NewSessionManager(memSessions{})
	s, err := sm.CreateSession(context.Background(), "u1", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &Registry{WhoAmI: SessionWhoAmI(sm, func(_ context.Context, id string) (*Account, error) {
		if id != "u1" {
			return nil, nil
		}
		return &Account{Email: "jack@test.com", Password: "secret", Label: "buyer", Phone: "+447700900123"}, nil
	})}
	h := r.Handler()

	get := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, Prefix+"/whoami", nil)
		req.Header.Set("X-Hamr-Actions-Token", "tok")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: sm.CookieName(), Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	t.Run("logged in", func(t *testing.T) {
		w := get(s.Token)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d", w.Code)
		}
		var out struct {
			Account map[string]string `json:"account"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Account["email"] != "jack@test.com" || out.Account["phone"] != "+447700900123" || out.Account["label"] != "buyer" {
			t.Errorf("account = %v", out.Account)
		}
		if _, ok := out.Account["password"]; ok {
			t.Error("whoami must never return the password")
		}
	})
	for name, cookie := range map[string]string{"no cookie": "", "unknown session": "nope"} {
		t.Run(name, func(t *testing.T) {
			w := get(cookie)
			if w.Code != http.StatusOK || w.Body.String() != "{\"account\":null}\n" {
				t.Errorf("code = %d body = %q", w.Code, w.Body.String())
			}
		})
	}
	t.Run("needs token", func(t *testing.T) {
		if w := do(h, http.MethodGet, Prefix+"/whoami", "", ""); w.Code != http.StatusUnauthorized {
			t.Errorf("code = %d", w.Code)
		}
	})
	t.Run("GET only", func(t *testing.T) {
		if w := do(h, http.MethodPost, Prefix+"/whoami", "tok", "{}"); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("code = %d", w.Code)
		}
	})
}

func TestWhoAmIUnsupportedAndErrors(t *testing.T) {
	setEnv(t, "tok", "staging")
	if w := do((&Registry{}).Handler(), http.MethodGet, Prefix+"/whoami", "tok", ""); w.Code != http.StatusNotFound {
		t.Errorf("no WhoAmI: code = %d, want 404", w.Code)
	}
	r := &Registry{WhoAmI: func(*http.Request) (*Account, error) { return nil, errors.New("db down") }}
	if w := do(r.Handler(), http.MethodGet, Prefix+"/whoami", "tok", ""); w.Code != http.StatusInternalServerError {
		t.Errorf("error: code = %d, want 500", w.Code)
	}
}

func TestRegisterReservesWhoAmI(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering an action named whoami must panic")
		}
	}()
	(&Registry{}).Register(Action{Name: "whoami"})
}
