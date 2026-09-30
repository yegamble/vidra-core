package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/sync/errgroup"
)

// ErrCopyUnavailable permits a streaming fallback only when no copy was started.
// Transport failures, source changes and partially completed copies are hard errors.
var ErrCopyUnavailable = errors.New("storage: server copy unavailable")

// ServerCopier transfers bytes without downloading them. It returns a size, never
// a content hash: an S3 ETag is not necessarily the SHA-256 of the copied bytes.
type ServerCopier interface {
	Copy(context.Context, Backend, string, string, int64) (int64, error)
}

type s3Copier struct {
	destination *S3
	client      *minio.Client
}

// NewS3Copier opts in with a separate credential that can read the source and
// write the destination. The ordinary source backend keeps its read-only key.
// B2 grants those capabilities across every bucket allowed by the copy key;
// the copy key itself is therefore not read-only on the source bucket.
// No endpoint override is accepted: the copier cannot redirect these credentials.
func NewS3Copier(destination Backend, accessKey, secretKey string) (ServerCopier, error) {
	if accessKey == "" && secretKey == "" {
		return nil, nil
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, fmt.Errorf("storage: server copy requires both credentials")
	}
	dest, ok := destination.(*S3)
	if !ok || dest == nil {
		return nil, fmt.Errorf("storage: server copy requires an S3 destination")
	}
	endpoint := dest.client.EndpointURL()
	client, err := minio.New(endpoint.Host, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: endpoint.Scheme == "https",
		Region: dest.region, BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: build server copy client: %w", err)
	}
	return &s3Copier{destination: dest, client: client}, nil
}

func (c *s3Copier) Copy(ctx context.Context, source Backend, sourceKey, destinationKey string, maxBytes int64) (int64, error) {
	if err := validateKey(sourceKey); err != nil {
		return 0, err
	}
	if err := validateKey(destinationKey); err != nil {
		return 0, err
	}
	src, ok := source.(*S3)
	dst := c.destination
	if !ok || src == nil || src.client.EndpointURL().String() != dst.client.EndpointURL().String() || src.region != dst.region {
		return 0, ErrCopyUnavailable
	}
	if src.bucket == dst.bucket && sourceKey == destinationKey {
		return 0, fmt.Errorf("storage: refusing server copy onto its source")
	}
	info, err := src.client.StatObject(ctx, src.bucket, sourceKey, minio.StatObjectOptions{})
	if err != nil {
		if s3err := minio.ToErrorResponse(err); s3err.Code == "NoSuchKey" || s3err.Code == "NoSuchObject" || s3err.Code == "NotFound" {
			return 0, ErrNotFound
		}
		return 0, classifyS3("copy-source-stat", err)
	}
	if info.Size < 0 || info.Size > maxBytes || info.ETag == "" {
		return 0, fmt.Errorf("storage: server copy source has invalid size, exceeds the size cap, or lacks an ETag")
	}
	// minio-go appends VersionID verbatim to the copy-source header query.
	// Opaque IDs containing +, / or = must survive query parsing unchanged.
	srcOpts := minio.CopySrcOptions{Bucket: src.bucket, Object: sourceKey, VersionID: url.QueryEscape(info.VersionID), MatchETag: strconv.Quote(info.ETag)}
	contentType := ContentTypeForKey(destinationKey)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var copied minio.UploadInfo
	if info.Size <= 4<<30 {
		// B2 rejects even an empty x-amz-tagging header and ignores tagging
		// directives. Keep tag replacement elsewhere; metadata always replaces.
		// https://www.backblaze.com/apidocs/s3-copy-object
		host := strings.TrimSuffix(strings.ToLower(dst.client.EndpointURL().Hostname()), ".")
		replaceTags := !strings.HasSuffix(host, ".backblazeb2.com")
		copied, err = c.client.CopyObject(ctx, minio.CopyDestOptions{
			Bucket: dst.bucket, Object: destinationKey, ReplaceMetadata: true, ReplaceTags: replaceTags, ContentType: contentType,
		}, srcOpts)
		if err != nil {
			return 0, copyStartError(ctx, err)
		}
	} else {
		copied, err = c.copyMultipart(ctx, srcOpts, destinationKey, contentType, info.Size)
		if err != nil {
			return 0, err
		}
	}
	// Verify the returned version rather than accidentally accepting a later
	// writer's object. Never claim success from a zero/empty copy response.
	verified, err := dst.client.StatObject(ctx, dst.bucket, destinationKey, minio.StatObjectOptions{VersionID: copied.VersionID})
	if err != nil {
		return 0, classifyS3("copy-destination-stat", err)
	}
	if copied.ETag == "" || verified.ETag != strings.Trim(copied.ETag, `"`) || verified.Size != info.Size || verified.ContentType != contentType {
		return 0, fmt.Errorf("storage: server copy destination verification failed")
	}
	return info.Size, nil
}

func (c *s3Copier) copyMultipart(ctx context.Context, src minio.CopySrcOptions, key, contentType string, size int64) (result minio.UploadInfo, err error) {
	core := minio.Core{Client: c.client}
	options := minio.PutObjectOptions{ContentType: contentType}
	uploadID, err := core.NewMultipartUpload(ctx, c.destination.bucket, key, options)
	if err != nil {
		return result, copyStartError(ctx, err)
	}
	if uploadID == "" {
		return result, fmt.Errorf("storage: multipart copy returned no upload ID")
	}
	complete := false
	defer func() {
		if !complete {
			// The copy's context may already be canceled. Bound independent cleanup
			// so a failed part cannot strand a billable incomplete upload silently.
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if abortErr := core.AbortMultipartUpload(cleanupCtx, c.destination.bucket, key, uploadID); abortErr != nil {
				err = errors.Join(err, fmt.Errorf("storage: abort multipart copy: %w", abortErr))
			}
		}
	}()
	headers := make(http.Header)
	src.Marshal(headers)
	metadata := make(map[string]string, len(headers))
	for name := range headers {
		metadata[name] = headers.Get(name)
	}
	// Keep control requests bounded; CopyObjectPart transfers bytes inside S3,
	// so parallel parts do not allocate payload-sized buffers on the importer.
	const partSize = 128 << 20
	parts := make([]minio.CompletePart, (size+partSize-1)/partSize)
	group, partCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for i := range parts {
		group.Go(func() error {
			if err := partCtx.Err(); err != nil {
				return err
			}
			offset := int64(i) * partSize
			part, err := core.CopyObjectPart(partCtx, src.Bucket, src.Object, c.destination.bucket, key, uploadID,
				i+1, offset, min(partSize, size-offset), metadata)
			if err != nil {
				return classifyS3("copy-part", err)
			}
			if part.ETag == "" {
				return fmt.Errorf("storage: multipart copy returned no part ETag")
			}
			parts[i] = part // Completion requires part-number order, not finish order.
			return nil
		})
	}
	// Join every in-flight request before completion or abort, including on cancel.
	if err := group.Wait(); err != nil {
		return result, err
	}
	result, err = core.CompleteMultipartUpload(ctx, c.destination.bucket, key, uploadID, parts, options)
	if err != nil {
		return result, classifyS3("complete-copy", err)
	}
	complete = true
	return result, nil
}

func copyStartError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		switch response.Code {
		case "AccessDenied", "NotImplemented", "NotSupported", "MethodNotAllowed":
			return errors.Join(ErrCopyUnavailable, err)
		}
	}
	return classifyS3("copy", err)
}
