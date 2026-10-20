package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// healthz says the process is up. It never touches the database, so a database
// outage does not make the orchestrator restart a healthy API.
func (h *H) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// readyz says the API can serve requests: the database answers.
func (h *H) readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := h.S.Pool.Ping(ctx); err != nil {
		_ = c.Error(err) // detail stays in server logs
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
