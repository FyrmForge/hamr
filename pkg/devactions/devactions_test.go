package devactions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testToken = "s3cret"

func setEnv(t *testing.T, token, env string) {
	t.Helper()
	t.Setenv("HAMR_ACTIONS_TOKEN", token)
	t.Setenv("HAMR_ACTIONS_ENV", env)
}

func newRegistry(disposable func(context.Context) (bool, error)) *Registry {
	r := &Registry{Disposable: disposable}
	r.Register(Action{
		Name: "create", Label: "Create", Description: "d",
		Inputs: []Input{
			{Name: "email", Label: "Email", Type: InputText, Required: true},
			{Name: "role", Label: "Role", Type: InputSelect, Options: []string{"admin", "user"}, Default: "user"},
			{Name: "n", Label: "N", Type: InputNumber},
			{Name: "flag", Label: "Flag", Type: InputBool},
		},
		Run: func(_ context.Context, in map[string]string) (string, error) {
			return "Created " + in["email"] + " as " + in["role"], nil
		},
	})
	r.Register(Action{Name: "boom", Run: func(context.Context, map[string]string) (string, error) {
		return "", errors.New("kaput")
	}})
	r.Register(Action{Name: "wipe", Dangerous: true, Run: func(context.Context, map[string]string) (string, error) {
		return "wiped", nil
	}})
	return r
}

func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Hamr-Actions-Token", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHandlerEnvGate(t *testing.T) {
	tests := []struct {
		name, token, env string
		wantNil          bool
	}{
		{"development", testToken, "development", false},
		{"staging", testToken, "staging", false},
		{"no token", "", "development", true},
		{"no env", testToken, "", true},
		{"production", testToken, "production", true},
		{"case sensitive", testToken, "Staging", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.token, tt.env)
			h := newRegistry(func(context.Context) (bool, error) { return true, nil }).Handler()
			if (h == nil) != tt.wantNil {
				t.Fatalf("Handler() nil = %v, want %v", h == nil, tt.wantNil)
			}
		})
	}
}

func TestHandlerPanicsDangerousWithoutDisposable(t *testing.T) {
	setEnv(t, testToken, "development")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	newRegistry(nil).Handler()
}

func TestHandlerNilGateSkipsDisposableCheck(t *testing.T) {
	setEnv(t, "", "")
	if newRegistry(nil).Handler() != nil {
		t.Fatal("expected nil handler")
	}
}

func TestRegisterPanics(t *testing.T) {
	tests := []struct{ name, action string }{{"empty", ""}, {"duplicate", "create"}, {"slash", "cache/clear"}, {"dot", "."}, {"dotdot", ".."}, {"space", "a b"}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			r := newRegistry(nil)
			r.Register(Action{Name: tt.action})
		})
	}
}

func TestToken(t *testing.T) {
	setEnv(t, testToken, "development")
	h := newRegistry(func(context.Context) (bool, error) { return true, nil }).Handler()
	tests := []struct {
		name, method, path, token string
		want                      int
	}{
		{"missing list", "GET", Prefix, "", 401},
		{"wrong list", "GET", Prefix, "nope", 401},
		{"missing post", "POST", Prefix + "/create", "", 401},
		{"unknown action still 401", "POST", Prefix + "/zzz", "nope", 401},
		{"ok", "GET", Prefix, testToken, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := do(h, tt.method, tt.path, tt.token, `{}`); w.Code != tt.want {
				t.Fatalf("got %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestList(t *testing.T) {
	setEnv(t, testToken, "staging")
	r := newRegistry(func(context.Context) (bool, error) { return true, nil })
	h := r.Handler()

	w := do(h, "GET", Prefix, testToken, "")
	var got struct {
		Actions []map[string]any `json:"actions"`
		// raw so we can tell [] from null
		Accounts json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if string(got.Accounts) != "[]" {
		t.Fatalf("accounts = %s, want []", got.Accounts)
	}
	if len(got.Actions) != 3 {
		t.Fatalf("actions = %d", len(got.Actions))
	}
	create, boom := got.Actions[0], got.Actions[1]
	for _, k := range []string{"name", "label", "description", "dangerous", "inputs"} {
		if _, ok := create[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if in, ok := boom["inputs"].([]any); !ok || len(in) != 0 {
		t.Errorf("inputs = %#v, want []", boom["inputs"])
	}
	inputs := create["inputs"].([]any)
	email, role := inputs[0].(map[string]any), inputs[1].(map[string]any)
	if _, ok := email["options"]; ok {
		t.Error("options must be omitted for non-select")
	}
	if opts, _ := role["options"].([]any); len(opts) != 2 {
		t.Errorf("select options = %v", role["options"])
	}
	if role["default"] != "user" || email["required"] != true || role["type"] != "select" {
		t.Errorf("unexpected input fields: %v %v", email, role)
	}

	r.Accounts = func(context.Context) ([]Account, error) {
		return []Account{{Email: "a@test.com", Password: "pw", Label: "admin"}}, nil
	}
	w = do(r.Handler(), "GET", Prefix, testToken, "")
	if !strings.Contains(w.Body.String(), `"accounts":[{"email":"a@test.com","password":"pw","label":"admin"}]`) {
		t.Fatalf("body = %s", w.Body)
	}

	r.Accounts = func(context.Context) ([]Account, error) { return nil, errors.New("db down") }
	if w = do(r.Handler(), "GET", Prefix, testToken, ""); w.Code != 500 {
		t.Fatalf("accounts error: got %d", w.Code)
	}
}

func TestRun(t *testing.T) {
	setEnv(t, testToken, "development")
	disposable := true
	var dispErr error
	h := newRegistry(func(context.Context) (bool, error) { return disposable, dispErr }).Handler()

	tests := []struct {
		name, method, path, body string
		disposable               bool
		dispErr                  error
		want                     int
		contains                 string
	}{
		{"ok with default", "POST", "/create", `{"inputs":{"email":"b@t.com"}}`, true, nil, 200, `"message":"Created b@t.com as user"`},
		{"ok explicit", "POST", "/create", `{"inputs":{"email":"b@t.com","role":"admin","n":"1.5","flag":"true"}}`, true, nil, 200, "as admin"},
		{"empty value uses default", "POST", "/create", `{"inputs":{"email":"b@t.com","role":""}}`, true, nil, 200, "as user"},
		{"required missing", "POST", "/create", `{"inputs":{}}`, true, nil, 400, "email is required"},
		{"bad select", "POST", "/create", `{"inputs":{"email":"x","role":"root"}}`, true, nil, 400, "role"},
		{"bad number", "POST", "/create", `{"inputs":{"email":"x","n":"abc"}}`, true, nil, 400, "number"},
		{"bad bool", "POST", "/create", `{"inputs":{"email":"x","flag":"yes"}}`, true, nil, 400, "true or false"},
		{"bad json", "POST", "/create", `{`, true, nil, 400, "invalid request body"},
		{"oversize body", "POST", "/create", `{"inputs":{"email":"` + strings.Repeat("a", maxBody) + `"}}`, true, nil, 400, "invalid request body"},
		{"unknown", "POST", "/nope", `{}`, true, nil, 404, "unknown action"},
		{"run error", "POST", "/boom", `{}`, true, nil, 500, `"error":"kaput"`},
		{"dangerous no confirm", "POST", "/wipe", `{}`, true, nil, 409, "confirmation required"},
		{"dangerous not disposable", "POST", "/wipe", `{"confirm":true}`, false, nil, 412, "not marked disposable"},
		{"dangerous disposable error", "POST", "/wipe", `{"confirm":true}`, false, errors.New("x"), 500, `"error":"x"`},
		{"dangerous ok", "POST", "/wipe", `{"confirm":true}`, true, nil, 200, `"message":"wiped"`},
		{"get on action", "GET", "/create", ``, true, nil, 405, ""},
		{"delete on action", "DELETE", "/create", ``, true, nil, 405, ""},
		{"post on list", "POST", "", `{}`, true, nil, 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disposable, dispErr = tt.disposable, tt.dispErr
			w := do(h, tt.method, Prefix+tt.path, testToken, tt.body)
			if w.Code != tt.want {
				t.Fatalf("got %d (%s), want %d", w.Code, w.Body, tt.want)
			}
			if !strings.Contains(w.Body.String(), tt.contains) {
				t.Fatalf("body %q missing %q", w.Body, tt.contains)
			}
			if w.Code != 405 && w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("content type = %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestRunReceivesOnlyDeclaredInputs(t *testing.T) {
	setEnv(t, testToken, "development")
	r := &Registry{}
	var seen map[string]string
	r.Register(Action{Name: "a", Inputs: []Input{{Name: "x", Type: InputText}}, Run: func(_ context.Context, in map[string]string) (string, error) {
		seen = in
		return "ok", nil
	}})
	do(r.Handler(), "POST", Prefix+"/a", testToken, `{"inputs":{"x":"1","evil":"2"}}`)
	if len(seen) != 1 || seen["x"] != "1" {
		t.Fatalf("seen = %v", seen)
	}
}

func TestOtherPathsNotHandled(t *testing.T) {
	setEnv(t, testToken, "development")
	h := newRegistry(func(context.Context) (bool, error) { return true, nil }).Handler()
	for _, p := range []string{"/", "/_devactionsX", "/other/_devactions"} {
		if w := do(h, "GET", p, testToken, ""); w.Code != 404 {
			t.Errorf("%s: got %d, want 404", p, w.Code)
		}
	}
}
