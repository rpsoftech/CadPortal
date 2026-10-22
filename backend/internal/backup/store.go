package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Object is one stored object as a listing shows it.
type Object struct {
	Key      string
	Size     int64
	Modified time.Time
}

// Meta is what must travel with an object so a copy behaves like the original: CAD files are stored
// gzipped and flagged Content-Encoding: gzip, photos and video are plain.
type Meta struct {
	ContentType     string
	ContentEncoding string
}

// Store is the part of an S3-compatible bucket the backup service uses.
type Store interface {
	List(ctx context.Context, prefix string) ([]Object, error)
	Get(ctx context.Context, key string) (io.ReadCloser, Meta, error)
	Put(ctx context.Context, key string, r io.Reader, m Meta) error
	Delete(ctx context.Context, key string) error
}

// S3Config describes a bucket. For Cloudflare R2 the endpoint is https://<account_id>.r2.cloudflarestorage.com
// and the region is "auto".
type S3Config struct {
	Endpoint, Region, Bucket, AccessKeyID, SecretAccessKey string
}

type S3 struct {
	bucket   string
	client   *s3.Client
	uploader *manager.Uploader
}

func NewS3(c S3Config) (*S3, error) {
	if c.Bucket == "" || c.Endpoint == "" || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("backup: endpoint, bucket and credentials are required")
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
	return &S3{bucket: c.Bucket, client: client, uploader: manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = 16 << 20
		u.Concurrency = 2 // a copy of a large video must not balloon the service's memory
	})}, nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	p := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			out = append(out, Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified)})
		}
	}
	return out, nil
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, Meta, error) {
	o, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, Meta{}, err
	}
	return o.Body, Meta{ContentType: aws.ToString(o.ContentType), ContentEncoding: aws.ToString(o.ContentEncoding)}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, m Meta) error {
	in := &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: r}
	if m.ContentType != "" {
		in.ContentType = aws.String(m.ContentType)
	}
	if m.ContentEncoding != "" {
		in.ContentEncoding = aws.String(m.ContentEncoding)
	}
	_, err := s.uploader.Upload(ctx, in)
	return err
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}

// Memory is an in-memory Store for tests.
type Memory struct {
	mu      sync.Mutex
	Objects map[string][]byte
	Metas   map[string]Meta
	When    map[string]time.Time
	// PutErr, when set for a key, makes Put of that key fail after reading the body.
	PutErr map[string]error
}

func NewMemory() *Memory {
	return &Memory{Objects: map[string][]byte{}, Metas: map[string]Meta{}, When: map[string]time.Time{}, PutErr: map[string]error{}}
}

func (m *Memory) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for k, v := range m.Objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, Object{Key: k, Size: int64(len(v)), Modified: m.When[k]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Objects[key]
	if !ok {
		return nil, Meta{}, fmt.Errorf("no such key %q", key)
	}
	return io.NopCloser(bytes.NewReader(b)), m.Metas[key], nil
}

func (m *Memory) Put(_ context.Context, key string, r io.Reader, meta Meta) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.PutErr[key]; err != nil {
		return err
	}
	m.Objects[key], m.Metas[key], m.When[key] = b, meta, time.Now()
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objects, key)
	delete(m.Metas, key)
	delete(m.When, key)
	return nil
}
