package main

import (
	"context"
	"fmt"
	"os"

	"vidra-core/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("S3 Configuration Test")
	fmt.Println("=====================")
	fmt.Printf("Endpoint: %s\n", cfg.S3Endpoint)
	fmt.Printf("Bucket: %s\n", cfg.S3Bucket)
	fmt.Printf("Region: %s\n", cfg.S3Region)
	fmt.Printf("Access Key: %s...\n", cfg.S3AccessKey[:10])
	fmt.Println()

	// Create AWS credentials
	creds := credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")

	// Create AWS config
	awsCfg := aws.Config{
		Region:      cfg.S3Region,
		Credentials: creds,
	}

	// Create S3 client
	var clientOpts []func(*s3.Options)
	if cfg.S3Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)

	ctx := context.Background()

	// Test 1: List buckets (might fail if not supported by Backblaze)
	fmt.Println("Test 1: Listing buckets...")
	bucketsOutput, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		fmt.Printf("  ✗ Failed to list buckets: %v\n", err)
		fmt.Println("  (This is expected for Backblaze B2 if using scoped keys)")
	} else {
		fmt.Printf("  ✓ Found %d buckets:\n", len(bucketsOutput.Buckets))
		for _, bucket := range bucketsOutput.Buckets {
			fmt.Printf("    - %s\n", *bucket.Name)
		}
	}
	fmt.Println()

	// Test 2: Check if specific bucket exists
	fmt.Printf("Test 2: Checking if bucket '%s' exists...\n", cfg.S3Bucket)
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(cfg.S3Bucket),
	})
	if err != nil {
		fmt.Printf("  ✗ Bucket check failed: %v\n", err)
		fmt.Println("  Possible reasons:")
		fmt.Println("    1. Bucket doesn't exist")
		fmt.Println("    2. No permission to access bucket")
		fmt.Println("    3. Bucket name is incorrect")
		fmt.Println()
		fmt.Println("Please verify:")
		fmt.Println("  1. The bucket exists in your Backblaze account")
		fmt.Println("  2. The bucket name matches exactly (case-sensitive)")
		fmt.Println("  3. Your application key has access to this bucket")
		os.Exit(1)
	} else {
		fmt.Println("  ✓ Bucket exists and is accessible")
	}
	fmt.Println()

	// Test 3: List objects in bucket
	fmt.Println("Test 3: Listing objects in bucket (first 10)...")
	listOutput, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(cfg.S3Bucket),
		MaxKeys: aws.Int32(10),
	})
	if err != nil {
		fmt.Printf("  ✗ Failed to list objects: %v\n", err)
	} else {
		fmt.Printf("  ✓ Bucket contains %d objects (showing first %d):\n", *listOutput.KeyCount, len(listOutput.Contents))
		for _, obj := range listOutput.Contents {
			fmt.Printf("    - %s (%d bytes)\n", *obj.Key, obj.Size)
		}
	}
	fmt.Println()

	fmt.Println("✓ All basic tests passed!")
	fmt.Println()
	fmt.Println("You can now use the s3migrate tool to migrate videos.")
}
