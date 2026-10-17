package flags

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevAction_SetsFlag(t *testing.T) {
	isolate(t)
	Int("devaction_test_limit", 7)
	c := New(NewMemStore())
	require.NoError(t, c.Start(context.Background()))
	a := DevAction(c)
	assert.Equal(t, "set-flag", a.Name)
	assert.False(t, a.Dangerous)
	assert.Contains(t, a.Inputs[0].Options, "devaction_test_limit")

	msg, err := a.Run(context.Background(), map[string]string{
		"key": "devaction_test_limit", "enabled": "true", "percent": "25", "subjects": " u1, u2 ,", "roles": "admin",
	})
	require.NoError(t, err)
	assert.Equal(t, "devaction_test_limit: on for 25%", msg)

	var got Rule
	for _, r := range c.Rules() {
		if r.Key == "devaction_test_limit" {
			got = r
		}
	}
	assert.True(t, got.Enabled)
	assert.Equal(t, 25, got.Percent)
	assert.Empty(t, got.Value, "empty value stores no value: the code default is served")
	assert.Equal(t, []string{"u1", "u2"}, got.Subjects)
	assert.Equal(t, []string{"admin"}, got.Roles)

	_, err = a.Run(context.Background(), map[string]string{"key": "devaction_test_limit", "value": "{nope"})
	assert.Error(t, err)
	_, err = a.Run(context.Background(), map[string]string{"key": "does_not_exist", "enabled": "true"})
	assert.Error(t, err, "Set rejects undeclared flags")
}

func TestDevAction_EmptyValueEnablesBool(t *testing.T) {
	isolate(t)
	f := Bool("devaction_test_bool", false)
	c := New(NewMemStore())
	require.NoError(t, c.Start(context.Background()))
	_, err := DevAction(c).Run(context.Background(), map[string]string{"key": "devaction_test_bool", "enabled": "true", "percent": "100"})
	require.NoError(t, err)
	assert.True(t, f.Get(c.For(context.Background(), Who{SubjectID: "u"})))
}

func TestDevAction_PercentDefault(t *testing.T) {
	isolate(t)
	Bool("devaction_test_pct", false)
	c := New(NewMemStore())
	require.NoError(t, c.Start(context.Background()))
	a := DevAction(c)
	percent := func() int {
		for _, r := range c.Rules() {
			if r.Key == "devaction_test_pct" {
				return r.Percent
			}
		}
		return -1
	}

	_, err := a.Run(context.Background(), map[string]string{"key": "devaction_test_pct", "enabled": "true"})
	require.NoError(t, err)
	assert.Equal(t, 100, percent(), "no targeting: everyone")

	_, err = a.Run(context.Background(), map[string]string{"key": "devaction_test_pct", "enabled": "true", "subjects": "u1"})
	require.NoError(t, err)
	assert.Equal(t, 0, percent(), "listed subjects only")

	_, err = a.Run(context.Background(), map[string]string{"key": "devaction_test_pct", "enabled": "true", "roles": "admin", "percent": "50"})
	require.NoError(t, err)
	assert.Equal(t, 50, percent(), "explicit percent wins")
}

func TestDevAction_MessageIncludesValue(t *testing.T) {
	isolate(t)
	Bool("devaction_test_msg_bool", true)
	Int("devaction_test_msg_int", 1)
	c := New(NewMemStore())
	require.NoError(t, c.Start(context.Background()))
	a := DevAction(c)
	run := func(in map[string]string) string {
		msg, err := a.Run(context.Background(), in)
		require.NoError(t, err)
		return msg
	}
	assert.Equal(t, "devaction_test_msg_bool: false for 100%", run(map[string]string{"key": "devaction_test_msg_bool", "enabled": "true", "value": "false"}))
	assert.Equal(t, "devaction_test_msg_int: 10 for 25%", run(map[string]string{"key": "devaction_test_msg_int", "enabled": "true", "value": "10", "percent": "25"}))
	assert.Equal(t, "devaction_test_msg_int: off", run(map[string]string{"key": "devaction_test_msg_int", "value": "10"}))
}
