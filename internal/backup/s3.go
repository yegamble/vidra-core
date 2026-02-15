package backup

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Backend struct {
	client    *s3.Client
	uploader  *manager.Uploader
	Bucket    string
	Region    string
	Prefix    string
	Endpoint  string
	accessKey string
	secretKey string
	once      sync.Once
	initErr   error
}

func NewS3Backend(bucket, region, prefix, endpoint, accessKey, secretKey string) *S3Backend {
	return &S3Backend{
		Bucket:    bucket,
		Region:    region,
		Prefix:    prefix,
		Endpoint:  endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

func (s *S3Backend) initClient(ctx context.Context) error {
	s.once.Do(func() {
		var opts []func(*awsconfig.LoadOptions) error

		opts = append(opts, awsconfig.WithRegion(s.Region))

		if s.accessKey != "" && s.secretKey != "" {
			opts = append(opts, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(s.accessKey, s.secretKey, ""),
			))
		}

		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			s.initErr = fmt.Errorf("loading AWS config: %w", err)
			return
		}

		clientOpts := []func(*s3.Options){}
		if s.Endpoint != "" {
			clientOpts = append(clientOpts, func(o *s3.Options) {
				o.BaseEndpoint = aws.String(s.Endpoint)
				o.UsePathStyle = true
			})
		}

		s.client = s3.NewFromConfig(cfg, clientOpts...)
		s.uploader = manager.NewUploader(s.client)
	})
	return s.initErr
}

func (s *S3Backend) buildKey(path string) string {
	if s.Prefix == "" {
		return path
	}

	prefix := s.Prefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return prefix + path
}

func (s *S3Backend) Upload(ctx context.Context, reader io.Reader, path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if err := s.initClient(ctx); err != nil {
		return err
	}

	key := s.buildKey(path)

	_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
		Body:   reader,
	})

	if err != nil {
		return fmt.Errorf("uploading to S3: %w", err)
	}

	return nil
}

func (s *S3Backend) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := s.initClient(ctx); err != nil {
		return nil, err
	}

	key := s.buildKey(path)

	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, fmt.Errorf("downloading from S3: %w", err)
	}

	return result.Body, nil
}

func (s *S3Backend) List(ctx context.Context, prefix string) ([]BackupEntry, error) {
	if err := s.initClient(ctx); err != nil {
		return nil, err
	}

	listPrefix := s.buildKey(prefix)

	var entries []BackupEntry

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.Bucket),
		Prefix: aws.String(listPrefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing S3 objects: %w", err)
		}

		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}

			relPath := strings.TrimPrefix(*obj.Key, s.Prefix)
			relPath = strings.TrimPrefix(relPath, "/")

			entries = append(entries, BackupEntry{
				Path:    relPath,
				Size:    aws.ToInt64(obj.Size),
				ModTime: aws.ToTime(obj.LastModified),
			})
		}
	}

	return entries, nil
}

func (s *S3Backend) Delete(ctx context.Context, path string) error {
	if err := s.initClient(ctx); err != nil {
		return err
	}

	key := s.buildKey(path)

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return fmt.Errorf("deleting from S3: %w", err)
	}

	return nil
}
