package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestStorageRoundTrip uploads and downloads a file through signed links, the way browsers do. It
// runs against the local stand-in (docker compose up -d), and is skipped when that isn't running.
// Point it at another service with STORAGE_TEST_ENDPOINT (and, for Google Cloud Storage,
// STORAGE_TEST_BUCKET, STORAGE_TEST_REGION=auto, STORAGE_TEST_ACCESS_KEY_ID and
// STORAGE_TEST_SECRET_ACCESS_KEY: an existing bucket and its HMAC keys).
func TestStorageRoundTrip(t *testing.T) {
	cfg := Config{
		Endpoint:        envOr("STORAGE_TEST_ENDPOINT", "http://localhost:8333"),
		Bucket:          envOr("STORAGE_TEST_BUCKET", "nms-storage-test"),
		Region:          envOr("STORAGE_TEST_REGION", "us-east-1"),
		AccessKeyID:     os.Getenv("STORAGE_TEST_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("STORAGE_TEST_SECRET_ACCESS_KEY"),
	}
	if cfg.Local() {
		if response, err := http.Get(cfg.Endpoint + "/healthz"); err != nil {
			t.Skip("the storage stand-in isn't running (docker compose up -d); skipping")
		} else {
			response.Body.Close()
		}
	}
	ctx := context.Background()
	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Local() {
		if err := s.PrepareLocalBucket(ctx, []string{"http://localhost:5173"}); err != nil {
			t.Fatal(err)
		}
	}
	key := "test/" + time.Now().Format("150405.000000") + ".jpg"
	t.Cleanup(func() { _ = s.Delete(context.Background(), key) })

	if _, exists, err := s.Stat(ctx, key); err != nil || exists {
		t.Fatalf("before upload: exists %v, %v; want no file", exists, err)
	}

	uploadURL, err := s.UploadURL(ctx, key, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	photo := []byte("not really a jpeg")
	request, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(photo))
	request.Header.Set("Content-Type", "image/jpeg")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload: status %d", response.StatusCode)
	}

	if info, exists, err := s.Stat(ctx, key); err != nil || !exists || info.Size != int64(len(photo)) || info.ContentType != "image/jpeg" {
		t.Fatalf("after upload: %+v, exists %v, %v; want %d bytes of image/jpeg", info, exists, err, len(photo))
	}

	viewURL, err := s.ViewURL(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	response, err = http.Get(viewURL)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Equal(downloaded, photo) {
		t.Errorf("downloaded %q, want %q", downloaded, photo)
	}

	// The API reads it too (the worker, to upload photos to Shopify).
	file, info, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(file)
	file.Close()
	if !bytes.Equal(read, photo) || info.Size != int64(len(photo)) || info.ContentType != "image/jpeg" {
		t.Errorf("Open read %q (%+v), want the photo as image/jpeg", read, info)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
