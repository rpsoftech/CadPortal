package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/export"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
	"github.com/rpsoftech/cad-portal/backend/internal/ziparchive"
)

// Order downloads: the CAD files an order recorded in one ZIP (GET /orders/:id/download?files=stl|3dm|stl,3dm)
// and the order package, which adds the job-bag PDF of every line and a summary CSV
// (GET /orders/:id/package?files=...). Both come in three forms, like the selection download:
//
//	GET /orders/:id/download            streams the ZIP (bearer token; for scripts)
//	GET /orders/:id/download/link       checks the order, tells files/bytes/missing/changed, returns a signed
//	                                    link (valid two minutes) that a browser can simply open
//	GET /downloads/order-zip/file?ticket=   the link itself, no bearer token
//
// and the same under /package. Every role may download; every download is audited as order.download.

const orderZipTicketTTL = 2 * time.Minute

func (h *H) orderIDParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequest(c, "invalid order id")
		return 0, false
	}
	return id, true
}

func (h *H) orderDownload(pack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := h.orderIDParam(c)
		if !ok {
			return
		}
		h.streamOrderZip(c, id, c.Query("files"), pack)
	}
}

func (h *H) orderDownloadLink(pack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := h.orderIDParam(c)
		if !ok {
			return
		}
		files := c.Query("files")
		plan, err := h.S.PlanOrderZip(c, id, files, pack)
		if err != nil {
			fail(c, err)
			return
		}
		now := time.Now()
		ticket, err := jwt.NewWithClaims(jwt.SigningMethodHS256, orderZipClaims{
			Role: auth.RoleFrom(c), OrderID: id, Files: strings.Join(plan.Kinds, ","), Package: pack,
			RegisteredClaims: jwt.RegisteredClaims{Subject: auth.UserIDFrom(c), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(orderZipTicketTTL))},
		}).SignedString(h.orderTicketKey())
		if err != nil {
			fail(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"url":                "/api/v1/downloads/order-zip/file?ticket=" + ticket,
			"expires_in_seconds": int(orderZipTicketTTL.Seconds()),
			"filename":           orderZipFilename(plan, now),
			"order":              plan.Order.Number,
			"lines":              len(plan.Order.Lines),
			"files":              plan.Files,
			"missing":            plan.Missing,
			"changed":            plan.Changed,
			"bytes":              plan.Bytes,
			"kinds":              plan.Kinds,
			"package":            pack,
		})
	}
}

func (h *H) orderZipFile(c *gin.Context) {
	var cl orderZipClaims
	tok, err := jwt.ParseWithClaims(c.Query("ticket"), &cl, func(*jwt.Token) (any, error) { return h.orderTicketKey(), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !tok.Valid || !cl.Role.Valid() || cl.Subject == "" || cl.IssuedAt == nil || cl.OrderID <= 0 {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired link"})
		return
	}
	if !h.ticketCallerOK(c, cl.Subject, cl.Role, cl.IssuedAt) {
		return
	}
	h.streamOrderZip(c, cl.OrderID, cl.Files, cl.Package)
}

func (h *H) streamOrderZip(c *gin.Context, orderID int64, files string, pack bool) {
	plan, err := h.S.PlanOrderZip(c, orderID, files, pack)
	if err != nil {
		fail(c, err)
		return
	}
	release, err := h.zipLimiter().Acquire(auth.UserIDFrom(c))
	if err != nil {
		c.Header("Retry-After", "30")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "you already have a download running; wait for it to finish"})
		return
	}
	defer release()

	extras := plan.Extras
	if pack {
		// The job-bag PDF is made before the first byte is sent, so a failure is a proper error answer
		// and not a broken ZIP. It is held in memory: at most MaxJobBags pages, each photo stored once.
		pdf, err := h.orderJobBagsPDF(c, orderID, plan.Order.Number)
		if err != nil {
			fail(c, err)
			return
		}
		extras = append([]ziparchive.Extra{{Name: plan.Order.Number + "-job-bags.pdf", Data: pdf}}, extras...)
	}

	hd := c.Writer.Header()
	hd.Set("Content-Type", "application/zip")
	hd.Set("Content-Disposition", storage.Disposition(orderZipFilename(plan, time.Now())))
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	res, err := ziparchive.Write(c.Request.Context(), c.Writer, plan.Entries, ziparchive.Options{
		Store: h.S.Store, MetaColumns: plan.MetaColumns, Comment: plan.Comment, Extras: extras,
	})
	detail := fmt.Sprintf("%s kinds=%s package=%t lines=%d files=%d missing=%d changed=%d bytes=%d problems=%d",
		plan.Order.Number, strings.Join(plan.Kinds, "+"), pack, len(plan.Order.Lines), res.Files, plan.Missing, plan.Changed, res.Bytes, res.Problems)
	if err != nil {
		detail += " aborted"
		log.Printf("order zip aborted for user %s: %v", auth.UserIDFrom(c), err)
	}
	auditNote(c, "order", idStr(orderID), detail)
}

func (h *H) orderJobBagsPDF(c *gin.Context, orderID int64, number string) ([]byte, error) {
	bags, err := h.S.OrderJobBags(c, orderID)
	if err != nil {
		return nil, err
	}
	b := export.NewJobBagsPDF(fmt.Sprintf("%s: %d job bags", number, len(bags)))
	for _, ob := range bags {
		ob.Photo = h.S.OrderBagPhoto(c, ob.ThumbKey)
		if err := b.AddBag(ob.JobBag); err != nil {
			return nil, err
		}
	}
	return b.Bytes()
}

func orderZipFilename(plan *service.OrderZipPlan, now time.Time) string {
	what := strings.Join(plan.Kinds, "-")
	if plan.Package {
		what = "package"
	}
	return fmt.Sprintf("%s-%s-%s.zip", plan.Order.Number, what, now.UTC().Format("20060102-1504"))
}

// ---------- signed link ----------

type orderZipClaims struct {
	Role    auth.Role `json:"role"`
	OrderID int64     `json:"o"`
	Files   string    `json:"f"`
	Package bool      `json:"p"`
	jwt.RegisteredClaims
}

// orderTicketKey is derived from the JWT secret and separated from the other keys by purpose, so an order
// link is neither a login token nor a selection-download link.
func (h *H) orderTicketKey() []byte {
	mac := hmac.New(sha256.New, h.JWTSecret)
	mac.Write([]byte("cadportal/order-zip-link/v1"))
	return mac.Sum(nil)
}
