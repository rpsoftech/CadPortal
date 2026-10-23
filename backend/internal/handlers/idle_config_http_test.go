package handlers

import (
	"encoding/json"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// The public config carries the idle timeout so operators change it without rebuilding the web app.
func TestAuthConfigCarriesTheIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, minutes := range []int{0, 30, 1440} {
		r := gin.New()
		(&H{S: &service.Service{}, JWTSecret: testSecret, IdleTimeoutMinutes: minutes}).Register(r)
		w := do(r, "GET", "/api/v1/auth/config", "", "", nil) // public: no token
		var got struct {
			Idle  *int  `json:"idle_timeout_minutes"`
			Reset *bool `json:"password_reset_email"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
			t.Fatalf("%d %s %v", w.Code, w.Body, err)
		}
		if got.Idle == nil || *got.Idle != minutes || got.Reset == nil || *got.Reset {
			t.Errorf("minutes=%d: %s", minutes, w.Body)
		}
	}
}
