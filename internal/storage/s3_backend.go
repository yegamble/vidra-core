package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Backend implements the StorageBackend interface for S3-compatible storage
// including AWS S3, Backblaze B2, DigitalOcean Spaces, etc.
type S3Backend struct {
	client     *s3.Client
	uploader   *manager.Uploader
	downloader *manager.Downloader
	bucket     string
	endpoint   string
	region     string
	publicURL  string
	pathStyle  bool
}

// S3Config holds configuration for S3-compatible storage
type S3Config struct {
	Endpoint  string // e.g., "https://s3.us-west-000.backblazeb2.com"
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	PublicURL string // Optional custom public URL for the bucket
	PathStyle bool   // Use path-style URLs (required for Backblaze)
}

// NewS3Backend creates a new S3-compatible storage backend
func NewS3Backend(cfg S3Config) (*S3Backend, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("access key and secret key are required")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	// Create AWS credentials
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")

	// Create S3 client config
	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: creds,
	}

	// Create S3 client with custom endpoint if provided (for Backblaze, DO Spaces, etc.)
	var clientOpts []func(*s3.Options)
	if cfg.Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = cfg.PathStyle
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)

	// Create uploader and downloader with optimized settings
	uploader := manager.NewUploader(client, func(u *manager.Uploader) {
		u.PartSize = 10 * 1024 * 1024 // 10MB parts
		u.Concurrency = 5
	})

	downloader := manager.NewDownloader(client, func(d *manager.Downloader) {
		d.PartSize = 10 * 1024 * 1024 // 10MB parts
		d.Concurrency = 5
	})

	// Set public URL
	publicURL := cfg.PublicURL
	if publicURL == "" {
		if cfg.Endpoint != "" {
			publicURL = fmt.Sprintf("%s/%s", cfg.Endpoint, cfg.Bucket)
		} else {
			publicURL = fmt.Sprintf("https://%s.s3.%s.amazonaws.com", cfg.Bucket, cfg.Region)
		}
	}

	return &S3Backend{
		client:     client,
		uploader:   uploader,
		downloader: downloader,
		bucket:     cfg.Bucket,
		endpoint:   cfg.Endpoint,
		region:     cfg.Region,
		publicURL:  publicURL,
		pathStyle:  cfg.PathStyle,
	}, nil
}

// Upload uploads data to S3
func (s *S3Backend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        data,
		ContentType: aws.String(contentType),
	}

	_, err := s.uploader.Upload(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	return nil
}

// UploadFile uploads a file from local filesystem to S3
func (s *S3Backend) UploadFile(ctx context.Context, key string, localPath string, contentType string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail the upload if file close fails
			_ = err
		}
	}()

	return s.Upload(ctx, key, file, contentType)
}

// Download downloads a file from S3
func (s *S3Backend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download from S3: %w", err)
	}

	return result.Body, nil
}

// GetURL returns the public URL for a file
func (s *S3Backend) GetURL(key string) string {
	return fmt.Sprintf("%s/%s", s.publicURL, key)
}

// GetSignedURL generates a presigned URL for temporary access
func (s *S3Backend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)

	request, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = expiration
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return request.URL, nil
}

// Delete removes a file from S3
func (s *S3Backend) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}

	return nil
}

// Exists checks if a file exists in S3
func (s *S3Backend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Check if it's a "not found" error using errors.As
		var notFound *types.NotFound
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &notFound) || errors.As(err, &noSuchKey) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if file exists: %w", err)
	}

	return true, nil
}

// Copy copies a file within S3
func (s *S3Backend) Copy(ctx context.Context, sourceKey, destKey string) error {
	copySource := fmt.Sprintf("%s/%s", s.bucket, sourceKey)

	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucket),
		CopySource: aws.String(copySource),
		Key:        aws.String(destKey),
	})
	if err != nil {
		return fmt.Errorf("failed to copy in S3: %w", err)
	}

	return nil
}

// GetMetadata retrieves metadata about a stored file
func (s *S3Backend) GetMetadata(ctx context.Context, key string) (*FileMetadata, error) {
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get metadata: %w", err)
	}

	metadata := &FileMetadata{
		Key:         key,
		Size:        aws.ToInt64(result.ContentLength),
		ContentType: aws.ToString(result.ContentType),
		ETag:        aws.ToString(result.ETag),
	}

	if result.LastModified != nil {
		metadata.LastModified = *result.LastModified
	}

	return metadata, nil
}

// DeleteMultiple deletes multiple files from S3 in a single request
func (s *S3Backend) DeleteMultiple(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	// S3 DeleteObjects API supports up to 1000 keys per request
	const maxKeysPerRequest = 1000

	for i := 0; i < len(keys); i += maxKeysPerRequest {
		end := i + maxKeysPerRequest
		if end > len(keys) {
			end = len(keys)
		}

		batch := keys[i:end]
		objects := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objects[j] = types.ObjectIdentifier{Key: aws.String(key)}
		}

		_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete multiple objects: %w", err)
		}
	}

	return nil
}
