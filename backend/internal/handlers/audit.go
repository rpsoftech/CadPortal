package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// auditRoute names what a route does for the audit log. Keys are "METHOD /full/path".
type auditRoute struct {
	action  string
	entity  string // entity type, e.g. "metal"
	idParam string // route parameter holding the entity id, if any
	reads   bool   // audit even though it is a GET (sensitive reads only)
	skip    bool   // recorded elsewhere
}

// Every state-changing route is listed so actions get stable names. A mutating route that
// is missing here is still recorded, under "<method> <path>", so a new endpoint is never
// silently unaudited. Request bodies are never recorded.
var auditRoutes = map[string]auditRoute{
	"POST /api/v1/auth/login":           {skip: true}, // recorded by the login handler (it knows the email)
	"POST /api/v1/auth/change-password": {action: "auth.password_change", entity: "user"},

	"POST /api/v1/auth/forgot-password":   {action: "auth.password_reset_request", entity: "user"},
	"POST /api/v1/auth/reset-password":    {action: "auth.password_reset", entity: "user"},
	"POST /api/v1/users/:id/reset-link":   {action: "user.reset_link", entity: "user", idParam: "id"},
	"POST /api/v1/users":                  {action: "user.create", entity: "user"},
	"PUT /api/v1/users/:id":               {action: "user.update", entity: "user", idParam: "id"},
	"POST /api/v1/users/:id/password":     {action: "user.password_reset", entity: "user", idParam: "id"},
	"POST /api/v1/masters/categories":     {action: "category.create", entity: "category"},
	"PUT /api/v1/masters/categories/:id":  {action: "category.update", entity: "category", idParam: "id"},
	"POST /api/v1/masters/tags":           {action: "tag.create", entity: "tag"},
	"PUT /api/v1/masters/tags/:id":        {action: "tag.update", entity: "tag", idParam: "id"},
	"POST /api/v1/masters/metals":         {action: "metal.create", entity: "metal"},
	"PUT /api/v1/masters/metals/:id":      {action: "metal.update", entity: "metal", idParam: "id"},
	"POST /api/v1/masters/stone-codes":    {action: "stone_code.create", entity: "stone_code"},
	"PUT /api/v1/masters/stone-codes/:id": {action: "stone_code.update", entity: "stone_code", idParam: "id"},
	"PUT /api/v1/masters/stone-matrix":    {action: "stone_matrix.update", entity: "stone_matrix"},
	"DELETE /api/v1/masters/stone-matrix": {action: "stone_matrix.delete_row", entity: "stone_matrix"},

	"POST /api/v1/designs":                    {action: "design.create", entity: "design"},
	"PUT /api/v1/designs/:id":                 {action: "design.update", entity: "design", idParam: "id"},
	"DELETE /api/v1/designs/:id":              {action: "design.delete", entity: "design", idParam: "id"},
	"POST /api/v1/designs/import":             {action: "design.import", entity: "design"},
	"POST /api/v1/designs/:id/variations":     {action: "variation.create", entity: "design", idParam: "id"},
	"PUT /api/v1/variations/:id/base-input":   {action: "variation.update_base", entity: "variation", idParam: "id"},
	"PUT /api/v1/variations/:id/seats":        {action: "variation.set_seats", entity: "variation", idParam: "id"},
	"POST /api/v1/variations/:id/clone-seats": {action: "variation.clone_seats", entity: "variation", idParam: "id"},
	"DELETE /api/v1/variations/:id":           {action: "variation.delete", entity: "variation", idParam: "id"},
	"POST /api/v1/variations/:id/upload-cad":  {action: "cad.upload", entity: "variation", idParam: "id"},
	"DELETE /api/v1/variations/:id/cad":       {action: "cad.remove", entity: "variation", idParam: "id"},
	"POST /api/v1/designs/:id/media":          {action: "media.upload", entity: "design", idParam: "id"},
	"DELETE /api/v1/media/:id":                {action: "media.delete", entity: "media", idParam: "id"},
	"PUT /api/v1/media/:id/cover":             {action: "media.cover", entity: "media", idParam: "id"},
	"POST /api/v1/trash/files/:id/restore":    {action: "trash.restore_file", entity: "trashed_file", idParam: "id"},
	"POST /api/v1/designs/:id/restore":        {action: "design.restore", entity: "design", idParam: "id"},
	"POST /api/v1/designs/:id/duplicate":      {action: "design.duplicate", entity: "design", idParam: "id"},
	"POST /api/v1/variations/:id/restore":     {action: "variation.restore", entity: "variation", idParam: "id"},
	"GET /api/v1/variations/:id/download":     {action: "cad.download_link", entity: "variation", idParam: "id", reads: true},
	"POST /api/v1/exports/job-bag-pdf":        {action: "export.job_bag", entity: "variation"},
	"POST /api/v1/exports/job-bags-pdf":       {action: "export.job_bags", entity: "variation"},
	"POST /api/v1/exports/costing-csv":        {action: "export.costing", entity: "variation"},
	"POST /api/v1/exports/costing-preview":    {skip: true}, // computes only, nothing leaves the screen
	"POST /api/v1/costing-sheets":             {action: "costing_sheet.create", entity: "costing_sheet"},
	"DELETE /api/v1/costing-sheets/:id":       {action: "costing_sheet.delete", entity: "costing_sheet", idParam: "id"},
	"POST /api/v1/costing-sheets/:id/recost":  {action: "costing_sheet.recost", entity: "costing_sheet", idParam: "id"},
	"GET /api/v1/costing-sheets/:id/csv":      {action: "costing_sheet.download", entity: "costing_sheet", idParam: "id", reads: true},
	"GET /api/v1/exports/designs-csv":         {action: "export.designs", entity: "design", reads: true},
	"POST /api/v1/orders":                     {action: "order.create", entity: "order"},
	"PUT /api/v1/orders/:id":                  {action: "order.update", entity: "order", idParam: "id"},
	"PUT /api/v1/orders/:id/lines":            {action: "order.set_lines", entity: "order", idParam: "id"},
	"POST /api/v1/orders/:id/status":          {action: "order.status", entity: "order", idParam: "id"},
	"POST /api/v1/orders/:id/clone":           {action: "order.clone", entity: "order", idParam: "id"},
	"GET /api/v1/orders/:id/summary.pdf":      {action: "order.summary_pdf", entity: "order", idParam: "id", reads: true},
	"GET /api/v1/orders/:id/job-bags.pdf":     {action: "order.job_bags", entity: "order", idParam: "id", reads: true},
	"GET /api/v1/orders/:id/csv":              {action: "order.csv", entity: "order", idParam: "id", reads: true},
	"GET /api/v1/exports/orders-csv":          {action: "export.orders", entity: "order", reads: true},
	"POST /api/v1/orders/:id/costing":         {action: "order.cost", entity: "order", idParam: "id"},
	"GET /api/v1/orders/:id/download":         {action: "order.download", entity: "order", idParam: "id", reads: true},
	"GET /api/v1/orders/:id/package":          {action: "order.download", entity: "order", idParam: "id", reads: true},
	"GET /api/v1/orders/:id/download/link":    {skip: true}, // checks and signs only; the download is recorded when it runs
	"GET /api/v1/orders/:id/package/link":     {skip: true},
	"GET /api/v1/downloads/order-zip/file":    {action: "order.download", entity: "order", reads: true},
	"POST /api/v1/backups/run":                {action: "backup.request", entity: "backup"},
	"GET /api/v1/backups/download":            {action: "backup.download_link", entity: "backup", reads: true},
	"POST /api/v1/downloads/cad-zip":          {action: "download.cad_zip", entity: "variation"},
	"POST /api/v1/downloads/cad-zip/link":     {skip: true}, // checks and signs only; the download is recorded when it runs
	"GET /api/v1/downloads/cad-zip/file":      {action: "download.cad_zip", entity: "variation", reads: true},
	"GET /api/v1/masters/export":              {action: "export.master_data", entity: "master_data", reads: true},
}

const auditNoteKey = "audit.note"

type auditNoteData struct{ entityType, entityID, detail string }

// auditNote lets a handler add what only it knows: the id of something it just created, or
// a short human-readable detail. Never put secrets or request bodies here.
func auditNote(c *gin.Context, entityType, entityID, detail string) {
	c.Set(auditNoteKey, auditNoteData{entityType, entityID, detail})
}

// audit records every state-changing request (and a few sensitive reads) after it ran.
// A failure to record is logged but never changes the response.
func (h *H) audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		route := c.FullPath()
		if route == "" { // no route matched (404): not an action
			return
		}
		method := c.Request.Method
		spec, known := auditRoutes[method+" "+route]
		if spec.skip {
			return
		}
		readOnly := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
		if readOnly && !spec.reads {
			return
		}
		status := c.Writer.Status()
		if status == http.StatusUnauthorized { // expired or missing token: no actor, only noise
			return
		}

		e := service.AuditEntry{Action: spec.action, EntityType: spec.entity, Status: status}
		if !known {
			e.Action = strings.ToLower(method) + " " + route
		}
		if spec.idParam != "" {
			e.EntityID = c.Param(spec.idParam)
		}
		if n, ok := c.Get(auditNoteKey); ok {
			if d, ok := n.(auditNoteData); ok {
				if d.entityType != "" {
					e.EntityType = d.entityType
				}
				if d.entityID != "" {
					e.EntityID = d.entityID
				}
				e.Detail = d.detail
			}
		}
		if uid, err := strconv.ParseInt(auth.UserIDFrom(c), 10, 64); err == nil {
			e.UserID = uid
		}

		// Detached from the request: the entry must be written even if the client went away.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
		defer cancel()
		if err := h.S.RecordAudit(ctx, e); err != nil {
			log.Printf("audit: %v", err)
		}
	}
}

// recordLogin writes the login outcome. The email of a failed attempt is kept (useful to
// spot someone guessing), the password never is.
func (h *H) recordLogin(c *gin.Context, ok bool, email string, userID int64, status int) {
	e := service.AuditEntry{Action: "auth.login", Status: status, UserID: userID}
	if !ok {
		e.Action, e.Email = "auth.login_failed", email
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
	defer cancel()
	if err := h.S.RecordAudit(ctx, e); err != nil {
		log.Printf("audit: %v", err)
	}
}

// listAudit is the admin-only view of the log, newest first, with paging.
func (h *H) listAudit(c *gin.Context) {
	f := service.AuditFilter{ActionPrefix: c.Query("action")}
	var err error
	if f.UserID, err = optInt(c, "user_id"); err != nil {
		badRequest(c, err.Error())
		return
	}
	if f.From, err = optTime(c, "from", false); err != nil {
		badRequest(c, err.Error())
		return
	}
	if f.To, err = optTime(c, "to", true); err != nil {
		badRequest(c, err.Error())
		return
	}
	for name, dst := range map[string]*int32{"limit": &f.Limit, "offset": &f.Offset} {
		v, perr := optInt(c, name)
		if perr != nil {
			badRequest(c, perr.Error())
			return
		}
		if v != nil {
			*dst = int32(*v)
		}
	}
	rows, total, err := h.S.ListAudit(c, f)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, rows)
}

// optTime reads an RFC 3339 timestamp or a plain date (YYYY-MM-DD). A plain date used as
// an upper bound means "through the end of that day", so it becomes the next midnight.
func optTime(c *gin.Context, name string, endOfDay bool) (*time.Time, error) {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, &timeParamError{name}
	}
	if endOfDay {
		t = t.Add(24 * time.Hour)
	}
	return &t, nil
}

type timeParamError struct{ name string }

func (e *timeParamError) Error() string {
	return e.name + " must be an RFC 3339 timestamp or a YYYY-MM-DD date"
}

func idStr(id int64) string { return strconv.FormatInt(id, 10) }
