// Package handlers wires the Gin REST API (v1) to the service layer.
package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/export"
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
	// TokenTTL is how long a login token lives (0 = DefaultTokenTTL).
	TokenTTL time.Duration
	// MaxUploadBytes caps a single CAD upload (0 = DefaultMaxUploadBytes).
	MaxUploadBytes int64
	// LocalFiles, when set (development storage), serves signed download links.
	LocalFiles *storage.Local
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

	v1 := r.Group("/api/v1")

	// Accounts
	v1.POST("/auth/login", h.login)
	v1.GET("/auth/me", anyRole, h.me)
	v1.POST("/auth/change-password", anyRole, h.changePassword)
	v1.GET("/users", admin, h.listUsers)
	v1.POST("/users", admin, h.createUser)
	v1.PUT("/users/:id", admin, h.updateUser)
	v1.POST("/users/:id/password", admin, h.resetPassword)

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

	// Designs, variations, seats
	v1.POST("/designs", editor, h.createDesign)
	v1.GET("/designs", anyRole, h.listDesigns)
	v1.GET("/designs/:code", anyRole, h.getDesign)
	v1.PUT("/designs/:id", editor, h.updateDesign)
	v1.DELETE("/designs/:id", editor, h.deleteDesign)
	v1.POST("/designs/:id/variations", editor, h.createVariation)
	v1.PUT("/variations/:id/base-input", editor, h.updateVariationBase)
	v1.GET("/variations/:id/seats", anyRole, h.listSeats)
	v1.PUT("/variations/:id/seats", editor, h.setSeats)
	v1.POST("/variations/:id/clone-seats", editor, h.cloneSeats)
	v1.GET("/variations/:id/simulate-bom", anyRole, h.simulateBOM)

	// CAD vault: uploads by admin/designer, downloads also by production.
	v1.POST("/variations/:id/upload-cad", editor, h.uploadCAD)
	v1.GET("/variations/:id/download", anyRole, h.downloadCAD)
	if h.LocalFiles != nil {
		// Auth is the signed, expiring link itself (like an R2 presigned URL).
		v1.GET("/local-files/*key", h.serveLocalFile)
	}

	// Exports: job bags for every role; costing exposes money, so admin/designer only.
	v1.POST("/exports/job-bag-pdf", anyRole, h.jobBagPDF)
	v1.POST("/exports/costing-csv", editor, h.costingCSV)
}

func fail(c *gin.Context, err error) {
	var mme *calc.MissingMatrixError
	if errors.As(err, &mme) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": mme.Error(), "missing": mme.Pairs})
		return
	}
	var mci *service.MissingCostInputsError
	if errors.As(err, &mci) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": mci.Error(),
			"metals_missing_wastage": mci.Wastage, "stone_rates_missing": mci.StoneRates})
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
	case http.StatusConflict:
		msg = strings.TrimPrefix(msg, "conflict: ")
	}
	if status == http.StatusInternalServerError {
		_ = c.Error(err) // keep detail in server logs only
		msg = "internal error"
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
		} `json:"rows"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	rows := make([]service.MatrixRowInput, len(in.Rows))
	for i, r := range in.Rows {
		rows[i] = service.MatrixRowInput{SizeMM: r.SizeMM, StoneCode: r.StoneCode,
			Sieve: r.Sieve, CaratsPerStone: r.CaratsPerStone, RatePerCt: r.RatePerCt}
	}
	if err := h.S.UpdateStoneMatrix(c, rows, in.BaselineCode); err != nil {
		fail(c, err)
		return
	}
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
	out, err := h.S.CreateDesign(c, in.input())
	respond(c, http.StatusCreated, out, err)
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
	f := service.DesignFilter{MetalCode: c.Query("metal_code")}
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
	respond(c, http.StatusOK, out, err)
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
		Items []struct {
			VariationID int64  `json:"variation_id"`
			MetalCode   string `json:"metal_code"`
			StoneCode   string `json:"stone_code"`
		} `json:"items"`
		GoldRatePerG     decimal.Decimal `json:"gold_rate_per_g"`
		SettingRatePerPc decimal.Decimal `json:"setting_rate_per_pc"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
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
	disp := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disp == "" {
		disp = "attachment"
	}
	hd := c.Writer.Header()
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Encoding", "gzip")
	hd.Set("Content-Disposition", disp)
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, "", st.ModTime(), f)
}
