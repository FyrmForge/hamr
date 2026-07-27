package middleware_test

import (
	"testing"

	"github.com/FyrmForge/hamr/pkg/middleware"
	"github.com/stretchr/testify/assert"
)

// The accessors a scaffolded Layout calls must survive the nil context an error
// page renders with — see pkg/ctx.Get. Before this, GET /favicon.ico on any
// project with flash/auth enabled 500'd with a recovered nil dereference
// instead of rendering the styled 404.
func TestSubjectAccessors_NilContext(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Empty(t, middleware.GetSubjectID(nil))
		assert.Nil(t, middleware.GetSubject(nil))
	})
}
