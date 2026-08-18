package web

import (
	"net/http"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestMaintenanceModeShedsTraffic(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		cfg := d.Config
		cfg.MaintenanceMode = true
		d.Config = cfg
	})

	// Pages get the 503 maintenance page.
	code, _, body := serve(t, s, "GET", "/", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "maintenance")

	// Probes stay live.
	code, _, body = serve(t, s, "GET", "/healthz", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"status":"ok"`)

	// Static assets stay live (the 503 page itself needs CSS).
	code, _, _ = serve(t, s, "GET", "/static/app.css", nil, nil)
	assert.Equal(t, http.StatusOK, code)

	// API paths get the JSON error shape, never HTML.
	code, _, body = serve(t, s, "GET", "/api/v1/projects", nil, nil)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, `"code":"maintenance"`)
}

func TestMaintenanceModeOffPassthrough(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		cfg := d.Config
		cfg.MaintenanceMode = false
		d.Config = cfg
	})
	code, _, body := serve(t, s, "GET", "/", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "GoGoGadget")
}

func TestMaintenanceModeParsesFromEnv(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("MAINTENANCE_MODE", "true")
	cfg, err := config.Load()
	assert.NoError(t, err)
	assert.True(t, cfg.MaintenanceMode)
}
