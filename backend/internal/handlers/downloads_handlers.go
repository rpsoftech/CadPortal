package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
	"github.com/rpsoftech/cad-portal/backend/internal/ziparchive"
)

// cadZipTicketTTL is how long a download link from POST /downloads/cad-zip/link can be opened.
const cadZipTicketTTL = 2 * time.Minute

type cadZipBody struct {
	VariationIDs []int64  `json:"variation_ids"`
	DesignIDs    []int64  `json:"design_ids"`
	Kinds        []string `json:"kinds"`
}

func (b cadZipBody) request() service.CADZipRequest {
	return service.CADZipRequest{VariationIDs: b.VariationIDs, DesignIDs: b.DesignIDs, Kinds: b.Kinds}
}

// zipLimiter is the one-download-per-user gate, shared by every route that streams a CAD ZIP.
func (h *H) zipLimiter() *ziparchive.Limiter {
	h.zipOnce.Do(func() { h.zips = ziparchive.NewLimiter() })
	return h.zips
}

// cadZip streams a ZIP of the STL / 3dm files of the chosen variations (or of all the live variations of
// the chosen designs): POST /api/v1/downloads/cad-zip with {variation_ids, design_ids, kinds}. The body of
// the answer is the ZIP itself; nothing is held in memory. A browser cannot send the bearer token with a
// plain navigation, so the web app uses cadZipLink and cadZipFile instead; this route serves scripts.
func (h *H) cadZip(c *gin.Context) {
	var in cadZipBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	h.streamCADZip(c, in.request())
}

// cadZipLink checks a selection, tells what it would download (files, bytes, what is missing) and returns
// a short-lived signed link for the browser to open. Opening the link saves the ZIP straight to disk.
func (h *H) cadZipLink(c *gin.Context) {
	var in cadZipBody
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	plan, err := h.S.PlanCADZip(c, in.request())
	if err != nil {
		fail(c, err)
		return
	}
	ticket, err := h.newCADZipTicket(c, plan)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"url":                "/api/v1/downloads/cad-zip/file?ticket=" + ticket,
		"expires_in_seconds": int(cadZipTicketTTL.Seconds()),
		"filename":           cadZipFilename(plan.Kinds, time.Now()),
		"variations":         plan.Variations,
		"files":              plan.Files,
		"missing":            plan.Missing,
		"bytes":              plan.Bytes,
		"kinds":              plan.Kinds,
	})
}

// cadZipFile is the signed link: it carries the chosen variations and kinds, the caller and an expiry, and
// needs no bearer token. The plan is made again from the live data, so a file replaced meanwhile is the
// current one.
func (h *H) cadZipFile(c *gin.Context) {
	tk, err := h.parseCADZipTicket(c.Query("ticket"))
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired link"})
		return
	}
	if !h.ticketCallerOK(c, tk.subject, tk.role, tk.issuedAt) {
		return
	}
	h.streamCADZip(c, service.CADZipRequest{VariationIDs: tk.variationIDs, Kinds: tk.kinds})
}

// ticketCallerOK checks, for a signed link, that the person it was made for may still sign in (not
// deactivated, password not changed since), and names them for the audit log. It answers the request itself
// when the check fails.
func (h *H) ticketCallerOK(c *gin.Context, subject string, role auth.Role, issuedAt *jwt.NumericDate) bool {
	cl := auth.Claims{Role: role, RegisteredClaims: jwt.RegisteredClaims{Subject: subject, IssuedAt: issuedAt}}
	if h.Validate != nil {
		ok, err := h.Validate(c.Request.Context(), cl)
		if err != nil {
			_ = c.Error(err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable"})
			return false
		}
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired link"})
			return false
		}
	}
	auth.SetCaller(c, subject, role) // so the audit log names the person
	return true
}

func (h *H) streamCADZip(c *gin.Context, req service.CADZipRequest) {
	plan, err := h.S.PlanCADZip(c, req)
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

	hd := c.Writer.Header()
	hd.Set("Content-Type", "application/zip")
	hd.Set("Content-Disposition", storage.Disposition(cadZipFilename(plan.Kinds, time.Now())))
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("X-Accel-Buffering", "no") // proxies must pass the bytes on as they come
	c.Status(http.StatusOK)

	// The request context ends when the client goes away, which stops the copy within one buffer.
	res, err := ziparchive.Write(c.Request.Context(), c.Writer, plan.Entries, ziparchive.Options{
		Store:       h.S.Store,
		MetaColumns: plan.MetaColumns,
		Comment:     "CAD files from the portal; MANIFEST.csv lists every file",
	})
	detail := fmt.Sprintf("kinds=%s variations=%d files=%d missing=%d bytes=%d problems=%d",
		strings.Join(plan.Kinds, "+"), plan.Variations, res.Files, plan.Missing, res.Bytes, res.Problems)
	if err != nil {
		detail += " aborted"
		log.Printf("cad zip aborted for user %s: %v", auth.UserIDFrom(c), err)
	}
	auditNote(c, "variation", "", detail)
}

func cadZipFilename(kinds []string, now time.Time) string {
	return fmt.Sprintf("cad-%s-%s.zip", strings.Join(kinds, "-"), now.UTC().Format("20060102-1504"))
}

// ---------- signed link ----------

type cadZipTicket struct {
	subject      string
	role         auth.Role
	variationIDs []int64
	kinds        []string
	issuedAt     *jwt.NumericDate
}

type cadZipClaims struct {
	Role  auth.Role `json:"role"`
	IDs   string    `json:"v"` // sorted variation ids, delta-coded varints, base64url: keeps the link short
	Kinds []string  `json:"k"`
	jwt.RegisteredClaims
}

// ticketKey is derived from the JWT secret and domain-separated, so a download link can never be used as a
// login token and the other way round.
func (h *H) ticketKey() []byte {
	mac := hmac.New(sha256.New, h.JWTSecret)
	mac.Write([]byte("cadportal/cad-zip-link/v1"))
	return mac.Sum(nil)
}

func (h *H) newCADZipTicket(c *gin.Context, plan *service.CADZipPlan) (string, error) {
	ids := slices.Clone(plan.VariationIDs)
	slices.Sort(ids)
	var raw []byte
	prev := int64(0)
	for _, id := range ids {
		raw = binary.AppendUvarint(raw, uint64(id-prev))
		prev = id
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, cadZipClaims{
		Role:  auth.RoleFrom(c),
		IDs:   base64.RawURLEncoding.EncodeToString(raw),
		Kinds: plan.Kinds,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: auth.UserIDFrom(c), IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(cadZipTicketTTL)),
		},
	}).SignedString(h.ticketKey())
}

func (h *H) parseCADZipTicket(raw string) (cadZipTicket, error) {
	var cl cadZipClaims
	tok, err := jwt.ParseWithClaims(raw, &cl, func(*jwt.Token) (any, error) { return h.ticketKey(), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil || !tok.Valid || !cl.Role.Valid() || cl.Subject == "" || cl.IssuedAt == nil {
		return cadZipTicket{}, errors.New("bad ticket")
	}
	b, err := base64.RawURLEncoding.DecodeString(cl.IDs)
	if err != nil {
		return cadZipTicket{}, err
	}
	var ids []int64
	prev := int64(0)
	for len(b) > 0 {
		d, n := binary.Uvarint(b)
		if n <= 0 {
			return cadZipTicket{}, errors.New("bad ticket ids")
		}
		b = b[n:]
		prev += int64(d)
		ids = append(ids, prev)
	}
	return cadZipTicket{subject: cl.Subject, role: cl.Role, variationIDs: ids, kinds: cl.Kinds, issuedAt: cl.IssuedAt}, nil
}

// zipState is embedded in H (see handlers.go) so the limiter is created on first use.
type zipState struct {
	zipOnce sync.Once
	zips    *ziparchive.Limiter
}
