// Package handlers wires the Gin REST API (v1) to the service layer.
package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/export"
	"github.com/rpsoftech/cad-portal/backend/internal/logx"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

type H struct {
	S *service.Service
	// JWTSecret verifies and signs bearer tokens.
	JWTSecret []byte
	// Validate is the per-request token check (user active, role unchanged, password
	// not changed since issue). Nil means signature and expiry only (tests).
	Validate auth.Validator
	// TokenTTL is how long a login token lives (0 = DefaultTokenTTL). This is the absolute cap on a
	// session: the idle timeout can end it sooner, but nothing extends it.
	TokenTTL time.Duration
	// IdleTimeoutMinutes is told to the web app (GET /auth/config), which signs the user out after this
	// long without activity. 0 = never. The server does not enforce it: the token expiry does.
	IdleTimeoutMinutes int
	// MaxUploadBytes caps a single CAD upload (0 = DefaultMaxUploadBytes).
	MaxUploadBytes int64
	// LocalFiles, when set (development storage), serves signed download links.
	LocalFiles *storage.Local

	zipState // one running CAD ZIP per user (see downloads_handlers.go)
}

const (
	DefaultMaxUploadBytes = 512 << 20
	DefaultTokenTTL       = 8 * time.Hour
	maxJSONBody           = 16 << 10 // auth payloads are tiny; refuse anything bigger
)

// Register mounts the API. Everything except login requires a valid token:
// reads for any signed-in role, writes for admin/designer, master data and user
// management for admin only.
func (h *H) Register(r *gin.Engine) {
	a := &auth.Authenticator{Secret: h.JWTSecret, Validate: h.Validate}
	anyRole := a.Require(auth.AllRoles...)
	editor := a.Require(auth.Editors...)
	admin := a.Require(auth.Admin)

	// Liveness and readiness for the container runtime; no auth, no data.
	r.GET("/healthz", h.healthz)
	r.GET("/readyz", h.readyz)

	// Every API call passes through the audit middleware (see audit.go).
	v1 := r.Group("/api/v1", h.audit())

	// Audit log (admin only)
	v1.GET("/audit", admin, h.listAudit)

	// Accounts
	v1.POST("/auth/login", h.login)
	v1.GET("/auth/me", anyRole, h.me)
	v1.POST("/auth/change-password", anyRole, h.changePassword)
	v1.GET("/auth/config", h.authConfig)
	v1.POST("/auth/forgot-password", h.forgotPassword)
	v1.POST("/auth/reset-password", h.resetWithToken)
	v1.GET("/users", admin, h.listUsers)
	v1.POST("/users", admin, h.createUser)
	v1.PUT("/users/:id", admin, h.updateUser)
	v1.POST("/users/:id/password", admin, h.resetPassword)
	v1.POST("/users/:id/reset-link", admin, h.adminResetLink)

	// Master data
	m := v1.Group("/masters")
	m.GET("/categories", anyRole, h.listCategories)
	m.POST("/categories", admin, h.createCategory)
	m.PUT("/categories/:id", admin, h.updateCategory)
	m.GET("/tags", anyRole, h.listTags)
	m.POST("/tags", admin, h.createTag)
	m.PUT("/tags/:id", admin, h.updateTag)
	m.GET("/metals", anyRole, h.listMetals)
	m.POST("/metals", admin, h.createMetal)
	m.PUT("/metals/:id", admin, h.updateMetal)
	m.GET("/stone-codes", anyRole, h.listStoneCodes)
	m.POST("/stone-codes", admin, h.createStoneCode)
	m.PUT("/stone-codes/:id", admin, h.setStoneCodeActive)
	m.GET("/stone-matrix", anyRole, h.listStoneMatrix)
	m.PUT("/stone-matrix", admin, h.updateStoneMatrix)
	m.DELETE("/stone-matrix", admin, h.deleteMatrixRow)
	m.GET("/matrix-gaps", admin, h.matrixGaps)
	m.GET("/recalc-status", admin, h.recalcStatus)
	m.GET("/setup-status", admin, h.setupStatus)
	m.GET("/export", admin, h.masterDataZip)

	// Designs, variations, seats
	v1.POST("/designs", editor, h.createDesign)
	v1.POST("/designs/import", editor, h.importDesigns)
	v1.GET("/designs", anyRole, h.listDesigns)
	v1.GET("/designs/:code", anyRole, h.getDesign)
	v1.PUT("/designs/:id", editor, h.updateDesign)
	v1.DELETE("/designs/:id", editor, h.deleteDesign)
	v1.POST("/designs/:id/duplicate", editor, h.duplicateDesign)
	v1.POST("/designs/:id/variations", editor, h.createVariation)
	v1.PUT("/variations/:id/base-input", editor, h.updateVariationBase)
	v1.DELETE("/variations/:id", editor, h.deleteVariation)
	v1.GET("/variations/:id/seats", anyRole, h.listSeats)
	v1.PUT("/variations/:id/seats", editor, h.setSeats)
	v1.POST("/variations/:id/clone-seats", editor, h.cloneSeats)
	v1.GET("/variations/:id/simulate-bom", anyRole, h.simulateBOM)

	// CAD vault: uploads by admin/designer, downloads also by production.
	v1.POST("/variations/:id/upload-cad", editor, h.uploadCAD)
	v1.GET("/variations/:id/download", anyRole, h.downloadCAD)
	v1.DELETE("/variations/:id/cad", editor, h.removeCAD)

	// Photos and video of a design or one of its variations: editors manage them, everyone sees them
	// (they arrive with GET /designs/:code).
	v1.POST("/designs/:id/media", editor, h.uploadMedia)
	v1.DELETE("/media/:id", editor, h.deleteMedia)
	v1.PUT("/media/:id/cover", editor, h.setCover)

	// Undo: deleted designs and removed or replaced files stay recoverable for a while.
	v1.GET("/trash", editor, h.listTrash)
	v1.POST("/trash/files/:id/restore", editor, h.restoreFile)
	v1.POST("/designs/:id/restore", editor, h.restoreDesign)
	v1.POST("/variations/:id/restore", editor, h.restoreVariation)
	if h.LocalFiles != nil {
		// Auth is the signed, expiring link itself (like an R2 presigned URL).
		v1.GET("/local-files/*key", h.serveLocalFile)
	}

	// Many CAD files in one ZIP: every role that may download a single file. The signed link is what a
	// browser opens (it cannot send the bearer token with a navigation), so it has no token middleware.
	v1.POST("/downloads/cad-zip", anyRole, h.cadZip)
	v1.POST("/downloads/cad-zip/link", anyRole, h.cadZipLink)
	v1.GET("/downloads/cad-zip/file", h.cadZipFile)

	// Backups: history, a "back up now" request and download links to the (encrypted) dumps. Admin only.
	v1.GET("/backups", admin, h.listBackups)
	v1.POST("/backups/run", admin, h.requestBackup)
	v1.GET("/backups/download", admin, h.downloadBackup)

	// Exports: job bags for every role; costing exposes money, so admin/designer only.
	v1.POST("/exports/job-bag-pdf", anyRole, h.jobBagPDF)
	v1.POST("/exports/job-bags-pdf", anyRole, h.jobBagsPDF)
	v1.POST("/exports/costing-csv", editor, h.costingCSV)
	v1.POST("/exports/costing-preview", editor, h.costingPreview)
	v1.POST("/costing-sheets", editor, h.createCostingSheet)
	v1.GET("/costing-sheets", editor, h.listCostingSheets)
	v1.GET("/costing-sheets/:id", editor, h.getCostingSheet)
	v1.GET("/costing-sheets/:id/csv", editor, h.costingSheetCSV)
	v1.DELETE("/costing-sheets/:id", editor, h.deleteCostingSheet)
	v1.POST("/costing-sheets/:id/recost", editor, h.recostCostingSheet)
	v1.GET("/exports/designs-csv", anyRole, h.designsCSV)

	// Orders: a selection of catalog variations sent to production. Everyone signed in reads (production
	// works from them); admin/designer write. Cancelled, never deleted.
	v1.POST("/orders", editor, h.createOrder)
	v1.GET("/orders", anyRole, h.listOrders)
	v1.GET("/orders/:id", anyRole, h.getOrder)
	v1.GET("/orders/:id/live", anyRole, h.orderLive)
	v1.PUT("/orders/:id", editor, h.updateOrder)
	v1.PUT("/orders/:id/lines", editor, h.setOrderLines)
	v1.POST("/orders/:id/status", editor, h.setOrderStatus)
	v1.POST("/orders/:id/clone", editor, h.cloneOrder)
	// The CAD files an order recorded, and the order package, as one ZIP. Every role may download; the signed
	// link is what a browser opens.
	v1.GET("/orders/:id/download", anyRole, h.orderDownload(false))
	v1.GET("/orders/:id/download/link", anyRole, h.orderDownloadLink(false))
	v1.GET("/orders/:id/package", anyRole, h.orderDownload(true))
	v1.GET("/orders/:id/package/link", anyRole, h.orderDownloadLink(true))
	v1.GET("/downloads/order-zip/file", h.orderZipFile)
}

func fail(c *gin.Context, err error) {
	var mme *calc.MissingMatrixError
	if errors.As(err, &mme) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": mme.Error(), "missing": mme.Pairs})
		return
	}
	var jbe *service.JobBagsError
	if errors.As(err, &jbe) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": jbe.Error(), "failures": jbe.Failures})
		return
	}
	var mci *service.MissingCostInputsError
	if errors.As(err, &mci) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": mci.Error(),
			"metals_missing_wastage": mci.Wastage, "stone_rates_missing": mci.StoneRates})
		return
	}
	var ice *service.IncompleteCostingError
	if errors.As(err, &ice) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": ice.Error(), "problems": ice.Problems,
			"metals_missing_wastage": ice.Wastage, "stone_rates_missing": ice.StoneRates, "missing": ice.Matrix})
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, service.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrStorageDisabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, service.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, service.ErrInvalidCredentials):
		status = http.StatusUnauthorized
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large"})
		return
	}
	msg := err.Error()
	// Internal sentinel text is not for users.
	switch status {
	case http.StatusBadRequest:
		msg = strings.TrimPrefix(msg, "invalid input: ")
	case http.StatusRequestEntityTooLarge:
		msg = strings.TrimPrefix(msg, "file too large: ")
	case http.StatusConflict:
		msg = strings.TrimPrefix(msg, "conflict: ")
	}
	if status == http.StatusInternalServerError {
		_ = c.Error(err) // keep detail in server logs only
		msg = "internal error"
		// The id lets someone find this request in the logs without being shown the detail.
		c.JSON(status, gin.H{"error": msg, "request_id": c.GetString(logx.RequestIDKey)})
		return
	}
	c.JSON(status, gin.H{"error": msg})
}

func badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": msg})
}

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

// ---------- Masters ----------

func (h *H) listCategories(c *gin.Context) {
	out, err := h.S.ListCategories(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) createCategory(c *gin.Context) {
	var in struct {
		Name       string `json:"name"`
		CodePrefix string `json:"code_prefix"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.CreateCategory(c, in.Name, in.CodePrefix)
	if err == nil {
		auditNote(c, "category", idStr(out.ID), out.Name)
	}
	respond(c, http.StatusCreated, out, err)
}

func (h *H) listTags(c *gin.Context) {
	list := h.S.ListTags
	if c.Query("include_inactive") == "true" && auth.RoleFrom(c) == auth.Admin {
		list = h.S.ListAllTags // only the admin screen needs deactivated tags
	}
	out, err := list(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) createTag(c *gin.Context) {
	var in struct {
		Name     string `json:"name"`
		TagGroup string `json:"tag_group"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.CreateTag(c, in.Name, in.TagGroup)
	if err == nil {
		auditNote(c, "tag", idStr(out.ID), out.TagGroup+": "+out.Name)
	}
	respond(c, http.StatusCreated, out, err)
}

func (h *H) listMetals(c *gin.Context) {
	out, err := h.S.ListMetals(c)
	if err == nil && auth.RoleFrom(c) == auth.Production {
		for i := range out { // wastage % is commercially sensitive
			out[i].WastagePct = decimal.NullDecimal{}
		}
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) updateMetal(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		SpecificGravity decimal.Decimal     `json:"specific_gravity"`
		WastagePct      decimal.NullDecimal `json:"wastage_pct"`
		IsDefaultInput  bool                `json:"is_default_input"`
		Active          bool                `json:"active"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.UpdateMetal(c, id, service.MetalUpdate{
		SpecificGravity: in.SpecificGravity, WastagePct: in.WastagePct,
		IsDefaultInput: in.IsDefaultInput, Active: in.Active,
	})
	if err == nil {
		wastage := "unset"
		if in.WastagePct.Valid {
			wastage = in.WastagePct.Decimal.String()
		}
		auditNote(c, "", "", fmt.Sprintf("%s: sg=%s wastage=%s default=%t active=%t",
			out.Code, in.SpecificGravity, wastage, in.IsDefaultInput, in.Active))
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) listStoneCodes(c *gin.Context) {
	out, err := h.S.ListStoneCodes(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) listStoneMatrix(c *gin.Context) {
	out, err := h.S.ListStoneMatrix(c)
	if err == nil && auth.RoleFrom(c) == auth.Production {
		for i := range out { // stone rates are commercially sensitive
			out[i].RatePerCt = decimal.NullDecimal{}
			out[i].RatePerPc = decimal.NullDecimal{}
			out[i].RatePerG = decimal.NullDecimal{}
		}
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) updateStoneMatrix(c *gin.Context) {
	var in struct {
		BaselineCode string `json:"baseline_code"`
		Rows         []struct {
			SizeMM         decimal.Decimal     `json:"size_mm"`
			StoneCode      string              `json:"stone_code"`
			Sieve          string              `json:"sieve"`
			CaratsPerStone decimal.Decimal     `json:"carats_per_stone"`
			RatePerCt      decimal.NullDecimal `json:"rate_per_ct"`
			RatePerPc      decimal.NullDecimal `json:"rate_per_pc"`
			RatePerG       decimal.NullDecimal `json:"rate_per_g"`
			RateBasis      string              `json:"rate_basis"`
		} `json:"rows"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	rows := make([]service.MatrixRowInput, len(in.Rows))
	for i, r := range in.Rows {
		rows[i] = service.MatrixRowInput{SizeMM: r.SizeMM, StoneCode: r.StoneCode,
			Sieve: r.Sieve, CaratsPerStone: r.CaratsPerStone, RatePerCt: r.RatePerCt,
			RatePerPc: r.RatePerPc, RatePerG: r.RatePerG, RateBasis: r.RateBasis}
	}
	if err := h.S.UpdateStoneMatrix(c, rows, in.BaselineCode); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "", "", fmt.Sprintf("%d rows, baseline=%q", len(rows), in.BaselineCode))
	// Weights are rebuilt asynchronously by the recalc worker.
	c.JSON(http.StatusAccepted, gin.H{"status": "saved", "recalc": "queued"})
}

// ---------- Designs ----------

type designBody struct {
	Code       string  `json:"code"`
	Name       string  `json:"name"`
	CategoryID int64   `json:"category_id"`
	TagIDs     []int64 `json:"tag_ids"`
}

func (b designBody) input() service.DesignInput {
	return service.DesignInput{Code: strings.TrimSpace(b.Code), Name: b.Name, CategoryID: b.CategoryID, TagIDs: b.TagIDs}
}

func (h *H) createDesign(c *gin.Context) {
	var in designBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	if strings.TrimSpace(in.Code) != "" {
		badRequest(c, "design codes are assigned automatically; leave code out")
		return
	}
	out, err := h.S.CreateDesign(c, in.input())
	if err == nil {
		auditNote(c, "design", idStr(out.ID), out.Code)
	}
	respond(c, http.StatusCreated, out, err)
}

// importDesigns validates (dry_run) or creates a batch of designs and variations from CSV rows
// the client parsed. Any problem returns 422 with every error and nothing is written.
func (h *H) importDesigns(c *gin.Context) {
	var in struct {
		DryRun bool                `json:"dry_run"`
		Rows   []service.ImportRow `json:"rows"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	res, err := h.S.ImportDesigns(c, in.Rows, in.DryRun)
	if err != nil {
		fail(c, err)
		return
	}
	if len(res.Errors) > 0 {
		auditNote(c, "design", "", fmt.Sprintf("rejected: %d problems", len(res.Errors)))
		c.JSON(http.StatusUnprocessableEntity, res)
		return
	}
	if in.DryRun {
		auditNote(c, "design", "", fmt.Sprintf("dry run: %d designs, %d variations", res.Designs, res.Variations))
		c.JSON(http.StatusOK, res)
		return
	}
	auditNote(c, "design", "", fmt.Sprintf("%d designs, %d variations", res.Designs, res.Variations))
	c.JSON(http.StatusCreated, res)
}

func (h *H) updateDesign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in designBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.UpdateDesign(c, id, in.input())
	respond(c, http.StatusOK, out, err)
}

func (h *H) deleteDesign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.S.DeleteDesign(c, id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *H) getDesign(c *gin.Context) {
	out, err := h.S.GetDesignByCode(c, c.Param("code"))
	respond(c, http.StatusOK, out, err)
}

func (h *H) listDesigns(c *gin.Context) {
	f := service.DesignFilter{MetalCode: c.Query("metal_code"), Query: c.Query("q"),
		StoneCode: c.Query("stone_code"), Sort: c.Query("sort")}
	var err error
	if f.CategoryID, err = optInt(c, "category_id"); err != nil {
		badRequest(c, err.Error())
		return
	}
	for _, s := range c.QueryArray("tag_ids[]") {
		id, perr := strconv.ParseInt(s, 10, 64)
		if perr != nil {
			badRequest(c, "tag_ids[] must be integers")
			return
		}
		f.TagIDs = append(f.TagIDs, id)
	}
	for name, dst := range map[string]**decimal.Decimal{
		"min_net_wt": &f.MinNetWt, "max_net_wt": &f.MaxNetWt,
		"min_gross_wt": &f.MinGrossWt, "max_gross_wt": &f.MaxGrossWt,
		"max_stone_cts": &f.MaxStoneCts,
		"min_stone_pct": &f.MinStonePct, "max_stone_pct": &f.MaxStonePct,
	} {
		if s := c.Query(name); s != "" {
			d, perr := decimal.NewFromString(s)
			if perr != nil {
				badRequest(c, name+" must be a number")
				return
			}
			*dst = &d
		}
	}
	if v, perr := optInt(c, "limit"); perr != nil {
		badRequest(c, perr.Error())
		return
	} else if v != nil {
		f.Limit = int32(*v)
	}
	if v, perr := optInt(c, "offset"); perr != nil {
		badRequest(c, perr.Error())
		return
	} else if v != nil {
		f.Offset = int32(*v)
	}
	out, err := h.S.ListDesigns(c, f)
	if err == nil {
		// Total matches before paging, so the client can render a paginator. 0 when the
		// requested page is past the end.
		total := int64(0)
		if len(out) > 0 {
			total = out[0].TotalCount
		}
		c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	}
	respond(c, http.StatusOK, out, err)
}

// duplicateDesign copies a design's category, tags, variations (base input and stone seats) into a
// new design with the next automatic code. CAD files and photos are never copied.
func (h *H) duplicateDesign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Name         string  `json:"name"`
		VariationIDs []int64 `json:"variation_ids"`
		CopyTags     *bool   `json:"copy_tags"`
	}
	if c.Request.ContentLength != 0 && c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	src, err := h.S.Q.GetDesignByID(c, id)
	if err != nil {
		fail(c, service.ErrNotFound)
		return
	}
	out, err := h.S.DuplicateDesign(c, id, service.DuplicateInput{
		Name: in.Name, VariationIDs: in.VariationIDs, CopyTags: in.CopyTags == nil || *in.CopyTags,
	})
	if err == nil {
		auditNote(c, "design", idStr(out.ID), "from "+src.Code)
	}
	respond(c, http.StatusCreated, out, err)
}

func (h *H) deleteVariation(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.S.DeleteVariation(c, id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func optInt(c *gin.Context, name string) (*int64, error) {
	s := c.Query(name)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, errors.New(name + " must be an integer")
	}
	return &v, nil
}

type baseBody struct {
	VolumeMM3    *decimal.Decimal `json:"volume_mm3"`
	RefWeightG   *decimal.Decimal `json:"ref_weight_g"`
	RefMetalCode string           `json:"ref_metal_code"`
}

func (h *H) createVariation(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in baseBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.CreateVariation(c, id, service.BaseInput(in))
	if err == nil {
		auditNote(c, "variation", idStr(out.ID), out.Code)
	}
	respond(c, http.StatusCreated, out, err)
}

func (h *H) updateVariationBase(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in baseBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	out, err := h.S.UpdateVariationBase(c, id, service.BaseInput(in))
	respond(c, http.StatusOK, out, err)
}

func respond(c *gin.Context, status int, body any, err error) {
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(status, body)
}

// ---------- Seats & BOM ----------

func (h *H) listSeats(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	out, err := h.S.ListSeats(c, id)
	respond(c, http.StatusOK, out, err)
}

func (h *H) setSeats(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Seats []struct {
			SizeMM          decimal.Decimal `json:"size_mm"`
			StoneCount      int32           `json:"stone_count"`
			LockedStoneCode string          `json:"locked_stone_code"`
		} `json:"seats"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	seats := make([]service.SeatInput, len(in.Seats))
	for i, st := range in.Seats {
		seats[i] = service.SeatInput(st)
	}
	out, err := h.S.SetSeats(c, id, seats)
	respond(c, http.StatusOK, out, err)
}

func (h *H) cloneSeats(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		SourceVariationID int64 `json:"source_variation_id"`
	}
	if c.ShouldBindJSON(&in) != nil || in.SourceVariationID <= 0 {
		badRequest(c, "source_variation_id is required")
		return
	}
	out, err := h.S.CloneSeats(c, id, in.SourceVariationID)
	respond(c, http.StatusOK, out, err)
}

func (h *H) simulateBOM(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	out, err := h.S.SimulateBOM(c, id, c.Query("code"))
	respond(c, http.StatusOK, out, err)
}

// ---------- CAD vault ----------

// uploadCAD streams the multipart "file" part straight into the store without
// buffering the whole body. The kind (.3dm / .stl) comes from the filename.
func (h *H) uploadCAD(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	limit := h.MaxUploadBytes
	if limit <= 0 {
		limit = DefaultMaxUploadBytes
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		badRequest(c, "expected multipart/form-data with a \"file\" part")
		return
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				badRequest(c, "missing \"file\" part")
			} else {
				fail(c, err)
			}
			return
		}
		if part.FormName() != "file" {
			continue
		}
		out, err := h.S.UploadCAD(c, id, part.FileName(), part)
		if err == nil {
			sha := out.SHA256
			if len(sha) > 12 {
				sha = sha[:12]
			}
			auditNote(c, "", "", fmt.Sprintf("%s %s sha256=%s", out.VariationCode, out.Filename, sha))
		}
		respond(c, http.StatusCreated, out, err)
		return
	}
}

func (h *H) downloadCAD(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	out, err := h.S.DownloadCAD(c, id, c.DefaultQuery("kind", "3dm"))
	respond(c, http.StatusOK, out, err)
}

// removeCAD detaches the 3dm or stl file of a variation and deletes its object.
func (h *H) removeCAD(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	kind := c.Query("kind")
	if err := h.S.RemoveCAD(c, id, kind); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "variation", idStr(id), kind)
	c.Status(http.StatusNoContent)
}

// ---------- Exports ----------

func (h *H) jobBagPDF(c *gin.Context) {
	var in struct {
		VariationID int64  `json:"variation_id"`
		MetalCode   string `json:"metal_code"`
		StoneCode   string `json:"stone_code"`
	}
	if c.ShouldBindJSON(&in) != nil || in.VariationID <= 0 {
		badRequest(c, "variation_id, metal_code and stone_code are required")
		return
	}
	jb, err := h.S.JobBag(c, in.VariationID, in.MetalCode, in.StoneCode)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "variation", idStr(in.VariationID), fmt.Sprintf("%s %s %s", jb.VariationCode, in.MetalCode, in.StoneCode))
	pdf, err := export.RenderJobBagPDF(jb)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "jobbag-"+jb.VariationCode+".pdf"))
	c.Data(http.StatusOK, "application/pdf", pdf)
}

func (h *H) costingCSV(c *gin.Context) {
	var in struct {
		service.SheetRequest
		Items []struct {
			VariationID int64  `json:"variation_id"`
			MetalCode   string `json:"metal_code"`
			StoneCode   string `json:"stone_code"`
		} `json:"items"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	if len(in.VariationIDs) > 0 || len(in.DesignIDs) > 0 {
		// Several designs / variations: one row per variation, a total row, incomplete ones per skip_incomplete.
		res, csv, ok := h.costRun(c, in.SheetRequest)
		if !ok {
			return
		}
		auditNote(c, "design", "", fmt.Sprintf("%d variations in %d designs, %d not costed",
			len(res.Rows), res.Totals.Designs, res.IncompleteVariations()))
		sendCostingCSV(c, res, csv, "costing.csv")
		return
	}
	req := service.CostingRequest{GoldRatePerG: in.GoldRatePerG, SettingRatePerPc: in.SettingRatePerPc}
	for _, it := range in.Items {
		req.Items = append(req.Items, service.CostingItem(it))
	}
	rows, err := h.S.CostingRows(c, req)
	if err != nil {
		fail(c, err)
		return
	}
	var buf bytes.Buffer
	if err := export.WriteCostingCSV(&buf, rows); err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="costing.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// designsCSV exports the catalog in the format the design import reads. It holds no wastage
// or rates, so every role may download it.
func (h *H) designsCSV(c *gin.Context) {
	rows, err := h.S.ExportDesigns(c)
	if err != nil {
		fail(c, err)
		return
	}
	var buf bytes.Buffer
	if err := export.WriteDesignsCSV(&buf, rows); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "design", "", fmt.Sprintf("%d rows", len(rows)))
	c.Header("Content-Disposition", `attachment; filename="designs.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// serveLocalFile streams a stored object after checking the signed link. The
// bytes are gzipped on disk, so Content-Encoding: gzip lets clients receive the
// original file, exactly as with R2.
func (h *H) serveLocalFile(c *gin.Context) {
	q := c.Request.URL.Query()
	key := strings.TrimPrefix(c.Param("key"), "/")
	filename := q.Get("filename")
	if !h.LocalFiles.Verify(key, filename, q.Get("expires"), q.Get("sig")) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired link"})
		return
	}
	f, err := h.LocalFiles.Open(key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		fail(c, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(c, err)
		return
	}
	hd := c.Writer.Header()
	if ct, inline := storage.InlineType(filename); inline {
		// A photo or video shown in the page: plain bytes, with range support for video.
		hd.Set("Content-Type", ct)
		hd.Set("Content-Disposition", "inline")
		hd.Set("Cache-Control", "private, max-age=3600")
	} else {
		hd.Set("Content-Type", "application/octet-stream")
		hd.Set("Content-Encoding", "gzip")
		hd.Set("Content-Disposition", storage.Disposition(filename))
		hd.Set("Cache-Control", "private, no-store")
	}
	hd.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, "", st.ModTime(), f)
}

// jobBagsPDF prints many job bags into one PDF. Every bag is checked before anything is drawn, so a
// missing stone-weight row anywhere is reported for all affected variations at once.
func (h *H) jobBagsPDF(c *gin.Context) {
	var in struct {
		Items []struct {
			VariationID int64  `json:"variation_id"`
			MetalCode   string `json:"metal_code"`
			StoneCode   string `json:"stone_code"`
		} `json:"items"`
		DesignIDs      []int64 `json:"design_ids"`
		MetalCode      string  `json:"metal_code"`
		StoneCode      string  `json:"stone_code"`
		SkipIncomplete bool    `json:"skip_incomplete"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	req := service.JobBagsRequest{DesignIDs: in.DesignIDs, MetalCode: in.MetalCode, StoneCode: in.StoneCode, SkipIncomplete: in.SkipIncomplete}
	for _, it := range in.Items {
		req.Items = append(req.Items, service.JobBagItem(it))
	}
	bags, skipped, err := h.S.PlanJobBags(c, req)
	if err != nil {
		fail(c, err)
		return
	}
	b := export.NewJobBagsPDF(fmt.Sprintf("%d job bags", len(bags)))
	for _, jb := range bags {
		jb.Photo = h.S.JobBagPhoto(c, jb) // one photo at a time; the PDF keeps each distinct photo once
		if err := b.AddBag(jb); err != nil {
			fail(c, err)
			return
		}
	}
	b.AddSkippedPage(skipped)
	pdf, err := b.Bytes()
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "variation", "", strconv.Itoa(len(bags)))
	if len(skipped) > 0 {
		codes := make([]string, len(skipped))
		for i, f := range skipped {
			codes[i] = f.VariationCode
		}
		c.Header("X-Skipped", strings.Join(codes, ","))
	}
	c.Header("Content-Disposition", `attachment; filename="jobbags.pdf"`)
	c.Data(http.StatusOK, "application/pdf", pdf)
}
