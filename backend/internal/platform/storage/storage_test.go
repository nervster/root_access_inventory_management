package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestLocalStackRoundTrip uploads and downloads a file through signed links, the way browsers do.
// It needs LocalStack (docker compose up -d) and is skipped when LocalStack isn't running.
func TestLocalStackRoundTrip(t *testing.T) {
	if response, err := http.Get("http://localhost:4566/_localstack/health"); err != nil {
		t.Skip("LocalStack isn't running (docker compose up -d); skipping")
	} else {
		response.Body.Close()
	}
	ctx := context.Background()
	s, err := New(ctx, Config{Bucket: "nms-storage-test", Region: "us-east-1", Endpoint: "http://localhost:4566"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareLocalBucket(ctx, []string{"http://localhost:5173"}); err != nil {
		t.Fatal(err)
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
}
