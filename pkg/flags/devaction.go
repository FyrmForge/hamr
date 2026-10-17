package flags

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/FyrmForge/hamr/pkg/devactions"
)

// DevAction returns a "set flag" action for the hamr dev widget.
// The flag list is read here, so declare flags before calling it.
func DevAction(c *Client) devactions.Action {
	var keys []string
	for _, d := range Definitions() {
		keys = append(keys, d.Key)
	}
	return devactions.Action{
		Name:        "set-flag",
		Label:       "Set feature flag",
		Description: "Turn a flag on or off, set its value and rollout.",
		Inputs: []devactions.Input{
			{Name: "key", Label: "Flag", Type: devactions.InputSelect, Options: keys, Required: true},
			{Name: "enabled", Label: "Enabled", Type: devactions.InputBool, Default: "true"},
			{Name: "value", Label: "Value (JSON; empty = on for bool, code default otherwise)", Type: devactions.InputText},
			{Name: "percent", Label: "Percent (empty = 0 when subjects or roles are given, else 100)", Type: devactions.InputNumber},
			{Name: "subjects", Label: "Subject IDs (comma separated)", Type: devactions.InputText},
			{Name: "roles", Label: "Roles (comma separated)", Type: devactions.InputText},
		},
		Run: func(ctx context.Context, in map[string]string) (string, error) {
			return runSetFlag(ctx, c, in)
		},
	}
}

func runSetFlag(ctx context.Context, c *Client, in map[string]string) (string, error) {
	key := in["key"]
	r := Rule{Key: key, Enabled: in["enabled"] == "true"}
	if v := strings.TrimSpace(in["value"]); v != "" {
		if !json.Valid([]byte(v)) {
			return "", fmt.Errorf("value is not valid JSON: %s", v)
		}
		r.Value = json.RawMessage(v)
	}
	r.Subjects, r.Roles = splitList(in["subjects"]), splitList(in["roles"])
	r.Percent = 100
	if len(r.Subjects) > 0 || len(r.Roles) > 0 {
		r.Percent = 0
	}
	if p := strings.TrimSpace(in["percent"]); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return "", fmt.Errorf("percent must be a whole number: %w", err)
		}
		r.Percent = n
	}
	if err := c.Set(ctx, r); err != nil {
		return "", err
	}
	state := "off"
	if r.Enabled {
		state = fmt.Sprintf("on for %d%%", r.Percent)
		if len(r.Value) > 0 {
			state = fmt.Sprintf("%s for %d%%", r.Value, r.Percent)
		}
	}
	return key + ": " + state, nil
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
