// Package storage keeps files, such as plant photos, in S3-compatible storage: Google Cloud Storage
// (through its S3-compatible API) when deployed, SeaweedFS locally; plain AWS S3 works too. Browsers
// upload and download directly with short-lived signed links; files never pass through the API.
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
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	uploadLinkLifetime = 15 * time.Minute
	viewLinkLifetime   = time.Hour
)

type Config struct {
	Bucket string
	Region string // "auto" for Google Cloud Storage
	// Endpoint is the storage service: https://storage.googleapis.com for Google Cloud Storage, or the
	// local stand-in (http://localhost:8333); empty means AWS S3.
	Endpoint string
	// PublicEndpoint is the address browsers use in signed links, when it differs from Endpoint
	// (the API in Docker reaches the stand-in at storage:8333, browsers at localhost:8333).
	PublicEndpoint string
	// AccessKeyID and SecretAccessKey sign requests: Google Cloud Storage's HMAC keys. Left empty, the
	// endpoint is the local stand-in (any keys work); with no endpoint either, AWS's usual credentials.
	AccessKeyID     string
	SecretAccessKey string
}

// Local reports whether this is the local stand-in: an endpoint, but no keys for it.
func (c Config) Local() bool { return c.Endpoint != "" && c.AccessKeyID == "" }

type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func New(ctx context.Context, cfg Config) (*S3, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	switch {
	case cfg.AccessKeyID != "":
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	case cfg.Local():
		// The local stand-in accepts any keys. (AWS S3 uses the environment's credentials.)
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	clientFor := func(endpoint string) *s3.Client {
		return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
				o.UsePathStyle = true // <endpoint>/<bucket>/<key>, which every S3-compatible service takes
				// The AWS library adds checksums by default that services other than AWS (Google Cloud
				// Storage among them) reject; only send them when an operation requires one.
				o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
				o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
				o.APIOptions = append(o.APIOptions, leaveAcceptEncodingUnsigned)
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

// leaveAcceptEncodingUnsigned keeps the Accept-Encoding header the AWS library adds out of request
// signatures: Google Cloud Storage's front end changes that header on the way in, so a signature
// that covers it fails with SignatureDoesNotMatch. The header is still sent, just not signed.
func leaveAcceptEncodingUnsigned(stack *middleware.Stack) error {
	if _, ok := stack.Finalize.Get("Signing"); !ok {
		return nil // a signed link, which doesn't sign Accept-Encoding
	}
	var acceptEncoding string
	drop := middleware.FinalizeMiddlewareFunc("DropAcceptEncoding", func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
		if request, ok := in.Request.(*smithyhttp.Request); ok {
			acceptEncoding = request.Header.Get("Accept-Encoding")
			request.Header.Del("Accept-Encoding")
		}
		return next.HandleFinalize(ctx, in)
	})
	restore := middleware.FinalizeMiddlewareFunc("RestoreAcceptEncoding", func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
		if request, ok := in.Request.(*smithyhttp.Request); ok && acceptEncoding != "" {
			request.Header.Set("Accept-Encoding", acceptEncoding)
		}
		return next.HandleFinalize(ctx, in)
	})
	if err := stack.Finalize.Insert(drop, "Signing", middleware.Before); err != nil {
		return err
	}
	return stack.Finalize.Insert(restore, "Signing", middleware.After)
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
// type here rather than trusting the upload link: not every service enforces what links were signed for.
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

// PrepareLocalBucket creates the bucket in the local stand-in if needed and lets browsers at origins
// upload to it. Deployed buckets are set up with the infrastructure instead (CORS on Google Cloud
// Storage is set with gcloud, not through the S3 API).
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
