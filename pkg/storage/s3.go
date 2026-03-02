package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Compile-time checks.
var _ SignableStorage = (*S3Storage)(nil)

// s3API is the subset of the S3 client used by S3Storage (unexported for testability).
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// presigner is the subset of the S3 presign client used by S3Storage.
type presigner interface {
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// S3Storage implements SignableStorage backed by an S3-compatible service
// (AWS S3, MinIO, Cloudflare R2).
type S3Storage struct {
	client    s3API
	presigner presigner
	bucket    string
	logger    *slog.Logger
}

// S3Option configures an S3Storage instance.
type S3Option func(*S3Storage)

// WithS3Logger sets the logger used by S3Storage.
func WithS3Logger(l *slog.Logger) S3Option {
	return func(s *S3Storage) { s.logger = l }
}

// NewS3Storage creates an S3Storage connected to the service described by cfg.
func NewS3Storage(cfg S3Config, opts ...S3Option) (*S3Storage, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("storage: S3 bucket must not be empty")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("storage: S3 region must not be empty")
	}

	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: endpointPtr(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: cfg.UsePathStyle,
	})

	ps := s3.NewPresignClient(client)

	return newS3StorageWithClient(client, ps, cfg.Bucket, opts...), nil
}

// newS3StorageWithClient allows tests to inject mock clients.
func newS3StorageWithClient(client s3API, ps presigner, bucket string, opts ...S3Option) *S3Storage {
	s := &S3Storage{
		client:    client,
		presigner: ps,
		bucket:    bucket,
		logger:    slog.Default(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// EnsureBucket creates the bucket if it does not already exist.
func (s *S3Storage) EnsureBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	if err == nil {
		return nil
	}
	_, err = s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket})
	if err != nil {
		return fmt.Errorf("storage: create bucket %q: %w", s.bucket, err)
	}
	s.logger.Info("bucket created", "bucket", s.bucket)
	return nil
}

func (s *S3Storage) Save(ctx context.Context, path string, r io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket,
		Key:    &path,
		Body:   r,
	})
	if err != nil {
		return fmt.Errorf("storage: s3 put %q: %w", path, err)
	}
	s.logger.Debug("file saved", "path", path, "bucket", s.bucket)
	return nil
}

func (s *S3Storage) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &path,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 get %q: %w", path, err)
	}
	return out.Body, nil
}

func (s *S3Storage) Delete(ctx context.Context, path string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &path,
	})
	if err != nil {
		return fmt.Errorf("storage: s3 delete %q: %w", path, err)
	}
	s.logger.Debug("file deleted", "path", path, "bucket", s.bucket)
	return nil
}

func (s *S3Storage) Exists(ctx context.Context, path string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &s.bucket,
		Key:    &path,
	})
	if err != nil {
		var notFound *s3types.NotFound
		var noSuchKey *s3types.NoSuchKey
		if errors.As(err, &notFound) || errors.As(err, &noSuchKey) {
			return false, nil
		}
		return false, fmt.Errorf("storage: s3 head %q: %w", path, err)
	}
	return true, nil
}

func (s *S3Storage) SignURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	req, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &path,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("storage: s3 presign %q: %w", path, err)
	}
	return req.URL, nil
}

func endpointPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
