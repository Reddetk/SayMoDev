package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler_ExposesBuildInfo(t *testing.T) {
	registry := NewRegistry("v1.2.3", "test")

	rec := httptest.NewRecorder()
	Handler(registry).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, rec.Body.String(), `billing_build_info{env="test",version="v1.2.3"} 1`)
	assert.Contains(t, rec.Body.String(), "go_goroutines")
}

func TestRegistry_IsIsolated(t *testing.T) {
	first := NewRegistry("a", "local")
	second := NewRegistry("b", "prod")

	families, err := first.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families)

	families, err = second.Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families)
}
