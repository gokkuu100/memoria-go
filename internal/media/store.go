// Package media implements the upload pipeline (presign → direct PUT →
// confirm) and signed-GET downloads. The bucket is private: a Store signed
// URL is the only way media bytes are ever read (SYSTEM_DESIGN §5).
package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"memoria-backend/internal/config"
)

// SignedURLTTL bounds how long any presigned PUT or GET stays valid.
const SignedURLTTL = 10 * time.Minute

// WidgetSignedURLTTL is longer — widget extensions cache feed JSON and may not refresh for hours.
const WidgetSignedURLTTL = 24 * time.Hour

// Store wraps two S3 clients over the same bucket: `internal` performs
// server-side operations (HEAD/GET/DELETE/bucket create) over the in-network
// endpoint, while presigned URLs handed to clients are signed against the
// public endpoint (SigV4 covers the Host header, so the URL must be signed
// for the host the client will actually hit).
type Store struct {
	internal       *s3.Client
	presign          *s3.PresignClient
	bucket           string
	awsCfg           aws.Config
	publicEndpoint   string
	usePathStyle     bool
}

func NewStore(ctx context.Context, cfg *config.Config) (*Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("loading s3 config: %w", err)
	}

	clientFor := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = cfg.S3UsePathStyle
		})
	}

	return &Store{
		internal:       clientFor(cfg.S3Endpoint),
		presign:          s3.NewPresignClient(clientFor(cfg.S3PublicEndpoint)),
		bucket:           cfg.S3Bucket,
		awsCfg:           awsCfg,
		publicEndpoint:   cfg.S3PublicEndpoint,
		usePathStyle:     cfg.S3UsePathStyle,
	}, nil
}

// EnsureBucket creates the bucket if it does not exist. Idempotent; fine for
// MinIO and acceptable on R2.
func (s *Store) EnsureBucket(ctx context.Context) error {
	_, err := s.internal.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	if err == nil {
		return nil
	}
	_, err = s.internal.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket})
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return nil
		}
	}
	return err
}

func (s *Store) presignClientFor(endpoint string) *s3.PresignClient {
	if endpoint == "" || endpoint == s.publicEndpoint {
		return s.presign
	}
	client := s3.NewFromConfig(s.awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = s.usePathStyle
	})
	return s3.NewPresignClient(client)
}

// PresignPut returns a presigned PUT URL signed for the configured public endpoint.
func (s *Store) PresignPut(ctx context.Context, key, contentType string) (string, error) {
	return s.PresignPutForPublicEndpoint(ctx, s.publicEndpoint, key, contentType)
}

// PresignPutForPublicEndpoint signs a PUT URL for a specific public endpoint.
func (s *Store) PresignPutForPublicEndpoint(ctx context.Context, publicEndpoint, key, contentType string) (string, error) {
	presign := s.presignClientFor(publicEndpoint)

	req, err := presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		ContentType: &contentType,
	}, s3.WithPresignExpires(SignedURLTTL))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// SignedURL returns a short-lived presigned GET URL for key — the only way
// media is ever read by clients. The URL points at the public endpoint.
func (s *Store) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	endpoint := publicEndpointFromContext(ctx, s.publicEndpoint)
	presign := s.presignClientFor(endpoint)

	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

// ObjectInfo is what confirm needs from a HEAD: the real stored size and type.
type ObjectInfo struct {
	ByteSize    int64
	ContentType string
}

// Head returns object metadata, or (nil, nil) when the object does not exist.
func (s *Store) Head(ctx context.Context, key string) (*ObjectInfo, error) {
	out, err := s.internal.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NotFound" || apiErr.ErrorCode() == "NoSuchKey") {
			return nil, nil
		}
		return nil, err
	}
	info := &ObjectInfo{}
	if out.ContentLength != nil {
		info.ByteSize = *out.ContentLength
	}
	if out.ContentType != nil {
		info.ContentType = *out.ContentType
	}
	return info, nil
}

// Open streams the object body (server-side only; used for image dimension
// probing at confirm time).
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.internal.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

// Delete removes the object; deleting a missing key is not an error.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.internal.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}

// Put streams an object into the bucket (server-side uploads, e.g. export ZIPs).
func (s *Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	in := &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	}
	if size >= 0 {
		in.ContentLength = &size
	}
	_, err := s.internal.PutObject(ctx, in)
	return err
}
