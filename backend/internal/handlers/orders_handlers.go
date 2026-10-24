package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// Orders: every signed-in role may read them (production works from them), admin and designer write.
// No prices are stored in an order, so there is nothing to hide from production.

// failOrder adds the order-specific 422 (every variation that cannot be ordered, listed) to fail.
func failOrder(c *gin.Context, err error) {
	var pe *service.OrderProblemsError
	if errors.As(err, &pe) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": pe.Error(), "problems": pe.Problems})
		return
	}
	fail(c, err)
}

func currentUserID(c *gin.Context) int64 {
	uid, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
	return uid
}

func (h *H) createOrder(c *gin.Context) {
	var in service.CreateOrderParams
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	in.UserID = currentUserID(c)
	o, err := h.S.CreateOrder(c, in)
	if err != nil {
		failOrder(c, err)
		return
	}
	auditNote(c, "order", idStr(o.ID), o.Number)
	c.JSON(http.StatusCreated, o)
}

func (h *H) listOrders(c *gin.Context) {
	var limit, offset int32
	for name, dst := range map[string]*int32{"limit": &limit, "offset": &offset} {
		v, err := optInt(c, name)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		if v != nil {
			*dst = int32(*v)
		}
	}
	rows, total, err := h.S.ListOrders(c, c.Query("status"), c.Query("q"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, rows)
}

func (h *H) getOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	o, err := h.S.GetOrder(c, id)
	respond(c, http.StatusOK, o, err)
}

func (h *H) updateOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	o, err := h.S.UpdateOrderHeader(c, id, in.Name, in.Notes)
	if err == nil {
		auditNote(c, "order", idStr(id), o.Number)
	}
	respond(c, http.StatusOK, o, err)
}

func (h *H) setOrderLines(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Items []service.OrderItem `json:"items"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	o, err := h.S.SetOrderLines(c, id, in.Items)
	if err != nil {
		failOrder(c, err)
		return
	}
	auditNote(c, "order", idStr(id), o.Number)
	c.JSON(http.StatusOK, o)
}

func (h *H) setOrderStatus(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	o, err := h.S.ChangeOrderStatus(c, id, in.Status, in.Note, currentUserID(c))
	if err == nil {
		auditNote(c, "order", idStr(id), o.Number+" -> "+in.Status)
	}
	respond(c, http.StatusOK, o, err)
}

// cloneOrder re-orders: a new order from an old one, every line re-snapshotted from the current
// catalog, with the differences from the old snapshots.
func (h *H) cloneOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in service.CloneOrderParams
	if c.Request.ContentLength != 0 && c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	in.UserID = currentUserID(c)
	o, diff, err := h.S.CloneOrder(c, id, in)
	if err != nil {
		failOrder(c, err)
		return
	}
	auditNote(c, "order", idStr(o.ID), o.Number+" cloned from "+idStr(id))
	c.JSON(http.StatusCreated, gin.H{"order": o, "diff": diff})
}
