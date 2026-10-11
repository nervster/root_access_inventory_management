// Package storage keeps files, such as plant photos, in S3: real S3 when deployed, LocalStack locally.
// Browsers upload and download directly with short-lived signed links; files never pass through the API.
package storage

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
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	uploadLinkLifetime = 15 * time.Minute
	viewLinkLifetime   = time.Hour
)

type Config struct {
	Bucket string
	Region string
	// Endpoint is set for LocalStack (http://localhost:4566); empty means real AWS S3.
	Endpoint string
	// PublicEndpoint is the address browsers use in signed links, when it differs from Endpoint
	// (the API in Docker reaches LocalStack at localstack:4566, browsers at localhost:4566).
	PublicEndpoint string
}

// Local reports whether this is LocalStack rather than real S3.
func (c Config) Local() bool { return c.Endpoint != "" }

type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func New(ctx context.Context, cfg Config) (*S3, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Local() {
		// LocalStack accepts any credentials; real S3 uses the environment's (e.g. the pod's IAM role).
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	clientFor := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
				o.UsePathStyle = true // http://localhost:4566/bucket/key, which LocalStack expects
			}
		})
	}
	client := clientFor(cfg.Endpoint)
	signer := client
	if cfg.PublicEndpoint != "" {
		signer = clientFor(cfg.PublicEndpoint)
	}
	return &S3{client: client, presign: s3.NewPresignClient(signer), bucket: cfg.Bucket}, nil
}

// UploadURL is a link a browser can PUT one file of contentType to, at key, for 15 minutes.
func (s *S3) UploadURL(ctx context.Context, key, contentType string) (string, error) {
	request, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(uploadLinkLifetime))
	if err != nil {
		return "", fmt.Errorf("sign upload link: %w", err)
	}
	return request.URL, nil
}

// ViewURL is a link a browser can download the file at key from, for an hour.
func (s *S3) ViewURL(ctx context.Context, key string) (string, error) {
	request, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)},
		s3.WithPresignExpires(viewLinkLifetime))
	if err != nil {
		return "", fmt.Errorf("sign view link: %w", err)
	}
	return request.URL, nil
}

// FileInfo is what's stored about an uploaded file.
type FileInfo struct {
	Size        int64
	ContentType string
}

// Stat describes the file at key, and returns false if there's no such file. Check the content
// type here rather than trusting the upload link: LocalStack doesn't enforce what links were signed for.
func (s *S3) Stat(ctx context.Context, key string) (FileInfo, bool, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return FileInfo{}, false, nil
	}
	if err != nil {
		return FileInfo{}, false, fmt.Errorf("check file: %w", err)
	}
	return FileInfo{Size: aws.ToInt64(head.ContentLength), ContentType: aws.ToString(head.ContentType)}, true, nil
}

// Open reads the file at key (e.g. for the worker to upload a photo to Shopify). Close it after.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, FileInfo, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, FileInfo{}, fmt.Errorf("read file: %w", err)
	}
	return object.Body, FileInfo{Size: aws.ToInt64(object.ContentLength), ContentType: aws.ToString(object.ContentType)}, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// PrepareLocalBucket creates the bucket in LocalStack if needed and lets browsers at origins upload
// to it. Deployed buckets are set up with the infrastructure instead.
func (s *S3) PrepareLocalBucket(ctx context.Context, origins []string) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})
	var owned *types.BucketAlreadyOwnedByYou
	if err != nil && !errors.As(err, &owned) {
		return fmt.Errorf("create bucket: %w", err)
	}
	_, err = s.client.PutBucketCors(ctx, &s3.PutBucketCorsInput{
		Bucket: aws.String(s.bucket),
		CORSConfiguration: &types.CORSConfiguration{CORSRules: []types.CORSRule{{
			AllowedOrigins: origins,
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
			MaxAgeSeconds:  aws.Int32(3600),
		}}},
	})
	if err != nil {
		return fmt.Errorf("allow browser uploads: %w", err)
	}
	return nil
}
