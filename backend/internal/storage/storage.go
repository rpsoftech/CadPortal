// Package storage abstracts the object store holding gzipped CAD files.
// The production implementation talks to Cloudflare R2 over the S3 API.
package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Store is the subset of object-store behaviour the service needs.
type Store interface {
	// Put streams r to key. The body is already gzipped by the caller.
	Put(ctx context.Context, key string, r io.Reader) error
	Delete(ctx context.Context, key string) error
	// PresignGet returns a time-limited download URL. The object is stored with
	// Content-Encoding: gzip, so clients receive the original file named filename.
	PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
	// PutMedia stores r under key exactly as it is, with contentType and no gzip: photos and video
	// are already compressed, and a video must be playable with range requests.
	PutMedia(ctx context.Context, key, contentType string, r io.Reader) error
	// PresignView returns a time-limited URL that shows the object inline with its content type
	// (for <img> and <video>), where PresignGet makes a download.
	PresignView(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	// Get opens a stored object for reading (the job bag reads a photo's thumbnail). The caller closes it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Config describes an S3-compatible bucket. For R2, Endpoint is
// https://<account_id>.r2.cloudflarestorage.com and Region is "auto".
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

type R2 struct {
	bucket   string
	client   *s3.Client
	presign  *s3.PresignClient
	uploader *manager.Uploader
}

func NewR2(c Config) (*R2, error) {
	if c.Bucket == "" || c.Endpoint == "" || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, fmt.Errorf("storage: endpoint, bucket and credentials are required")
	}
	if c.Region == "" {
		c.Region = "auto"
	}
	client := s3.New(s3.Options{
		Region:       c.Region,
		BaseEndpoint: aws.String(c.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
		UsePathStyle: true,
		// R2 does not support the SDK's default trailer checksums.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &R2{
		bucket:   c.Bucket,
		client:   client,
		presign:  s3.NewPresignClient(client),
		uploader: manager.NewUploader(client),
	}, nil
}

func (r *R2) Put(ctx context.Context, key string, body io.Reader) error {
	_, err := r.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:          &r.bucket,
		Key:             &key,
		Body:            body, // streamed in multipart chunks; never fully buffered
		ContentEncoding: aws.String("gzip"),
		ContentType:     aws.String("application/octet-stream"),
	})
	return err
}

func (r *R2) Delete(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &r.bucket, Key: &key})
	return err
}

func (r *R2) PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	req, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     &r.bucket,
		Key:                        &key,
		ResponseContentDisposition: aws.String(Disposition(filename)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// Memory is an in-memory Store for tests.
type Memory struct {
	mu      sync.Mutex
	Objects map[string][]byte
	// PutErr, when set, is returned by Put after the body has been read.
	PutErr error
}

func NewMemory() *Memory { return &Memory{Objects: map[string][]byte{}} }

func (m *Memory) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.PutErr != nil {
		return m.PutErr
	}
	m.Objects[key] = b
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objects, key)
	return nil
}

func (m *Memory) PresignGet(_ context.Context, key, filename string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("memory://%s?filename=%s&ttl=%s", key, filename, ttl), nil
}

// Keys returns the stored keys (test helper).
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.Objects))
	for k := range m.Objects {
		out = append(out, k)
	}
	return out
}

// Disposition is the Content-Disposition value for a download named filename. mime.FormatMediaType
// writes a plain filename for ASCII and the RFC 2231 form (filename*=utf-8”...) for anything
// else, so names with accents, spaces or quotes arrive intact and cannot inject header text.
// The local-disk handler uses the same function, so both stores answer alike.
func Disposition(filename string) string {
	if d := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); d != "" {
		return d
	}
	return "attachment"
}

func (r *R2) PutMedia(ctx context.Context, key, contentType string, body io.Reader) error {
	_, err := r.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      &r.bucket,
		Key:         &key,
		Body:        body, // streamed in multipart chunks, so a long video is never held in memory
		ContentType: aws.String(contentType),
	})
	return err
}

func (r *R2) PresignView(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	req, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     &r.bucket,
		Key:                        &key,
		ResponseContentType:        aws.String(contentType),
		ResponseContentDisposition: aws.String("inline"),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (m *Memory) PutMedia(ctx context.Context, key, _ string, r io.Reader) error {
	return m.Put(ctx, key, r)
}

func (m *Memory) PresignView(_ context.Context, key, contentType string, ttl time.Duration) (string, error) {
	return fmt.Sprintf("memory://%s?view=%s&ttl=%s", key, contentType, ttl), nil
}

func (r *R2) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &r.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Objects[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// ErrNotFound is what Get reports for an object that is not there (Memory wraps it; Local and R2 report
// their own errors, which IsNotFound also recognises).
var ErrNotFound = errors.New("storage: no such object")

// IsNotFound reports whether err means "the object does not exist", for any store.
func IsNotFound(err error) bool {
	var nsk *types.NoSuchKey
	var nf *types.NotFound
	return errors.Is(err, ErrNotFound) || errors.Is(err, fs.ErrNotExist) || errors.As(err, &nsk) || errors.As(err, &nf)
}

// originalGetter is implemented by a store that knows how its objects are encoded on the wire.
type originalGetter interface {
	GetOriginal(ctx context.Context, key string) (io.ReadCloser, error)
}

// OpenOriginal opens a gzipped CAD object and returns the ORIGINAL file bytes (what was uploaded), whichever
// store holds it. CAD objects are always stored gzipped: Memory and Local hand back the stored bytes, so
// they are gunzipped here; R2 serves them with Content-Encoding: gzip, which Go's HTTP client may already
// have decoded (see R2.GetOriginal). The caller closes the result.
func OpenOriginal(ctx context.Context, s Store, key string) (io.ReadCloser, error) {
	if og, ok := s.(originalGetter); ok {
		return og.GetOriginal(ctx, key)
	}
	rc, err := s.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return gunzipCloser(rc)
}

// gunzipCloser wraps a gzip stream; closing the result closes the underlying body too.
func gunzipCloser(rc io.ReadCloser) (io.ReadCloser, error) {
	zr, err := gzip.NewReader(rc)
	if err != nil {
		rc.Close()
		return nil, fmt.Errorf("storage: object is not valid gzip: %w", err)
	}
	return &gzipBody{zr: zr, body: rc}, nil
}

type gzipBody struct {
	zr   *gzip.Reader
	body io.Closer
}

func (g *gzipBody) Read(p []byte) (int, error) { return g.zr.Read(p) }
func (g *gzipBody) Close() error {
	err := g.zr.Close()
	if cerr := g.body.Close(); err == nil {
		err = cerr
	}
	return err
}

// GetOriginal returns the original bytes of a CAD object. The object is stored with Content-Encoding:
// gzip. Go's HTTP transport asks for gzip by itself and, when the server answers with it, decodes the body
// and drops the Content-Encoding header; in that case the body already is the original file. When the header
// is still there (a client that does not decode) the body is the gzip stream and is decoded here. Either
// way the caller gets the original bytes.
func (r *R2) GetOriginal(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &r.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	return decodeStored(out.Body, aws.ToString(out.ContentEncoding))
}

func decodeStored(body io.ReadCloser, contentEncoding string) (io.ReadCloser, error) {
	if contentEncoding == "gzip" {
		return gunzipCloser(body)
	}
	return body, nil
}
