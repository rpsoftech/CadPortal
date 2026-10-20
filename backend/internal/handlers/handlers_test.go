package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

func TestFailMapsMissingMatrixTo422(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	fail(c, &calc.MissingMatrixError{Pairs: []calc.MissingPair{{SizeMM: "1.50", Code: "1HD"}}})

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Missing []calc.MissingPair `json:"missing"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Missing) != 1 || body.Missing[0].Code != "1HD" {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func mustDec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
