package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// TestLocalStackQueue sends a job through SQS, retries it until SQS gives up, and finds it in the
// dead-letter queue. It needs LocalStack (docker compose up -d) and is skipped when it isn't running.
func TestLocalStackQueue(t *testing.T) {
	if response, err := http.Get("http://localhost:4566/_localstack/health"); err != nil {
		t.Skip("LocalStack isn't running (docker compose up -d); skipping")
	} else {
		response.Body.Close()
	}
	ctx := t.Context()
	name := "jobs-test-" + time.Now().Format("150405000000")
	queue, err := NewSQS(ctx, SQSConfig{Queue: name, Region: "us-east-1", Endpoint: "http://localhost:4566"})
	if err != nil {
		t.Fatal(err)
	}
	deadURL, err := queue.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name + "-dead")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, url := range []*string{aws.String(queue.url), deadURL.QueueUrl} {
			_, _ = queue.client.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: url})
		}
	})

	job := Job{ID: 42, OrganizationID: 7, Kind: "test.kind", Payload: json.RawMessage(`{"productId":12}`)}
	if err := queue.Send(ctx, []Job{job}); err != nil {
		t.Fatal(err)
	}

	// Each failure makes it come back (right away here), counting attempts.
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		deliveries, err := queue.Receive(ctx)
		if err != nil || len(deliveries) != 1 {
			t.Fatalf("attempt %d: received %d, %v; want the job", attempt, len(deliveries), err)
		}
		got := deliveries[0].Job
		if got.ID != 42 || got.OrganizationID != 7 || got.Kind != "test.kind" || string(got.Payload) != `{"productId":12}` || got.Attempt != attempt {
			t.Fatalf("attempt %d: received %+v (payload %s)", attempt, got, got.Payload)
		}
		if err := queue.RetryLater(ctx, deliveries[0], 0); err != nil {
			t.Fatal(err)
		}
	}

	// After the last attempt, SQS moves it to the dead-letter queue (on the next receive).
	_, _ = queue.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: aws.String(queue.url), WaitTimeSeconds: 1})
	dead, err := queue.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: deadURL.QueueUrl, WaitTimeSeconds: 5})
	if err != nil || len(dead.Messages) != 1 {
		t.Fatalf("dead-letter queue has %d, %v; want the job", len(dead.Messages), err)
	}

	// Done removes a job for good.
	if err := queue.Send(ctx, []Job{{ID: 43, Kind: "test.kind", Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	deliveries, err := queue.Receive(ctx)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("received %d, %v; want job 43", len(deliveries), err)
	}
	if err := queue.Done(ctx, deliveries[0]); err != nil {
		t.Fatal(err)
	}
	attributes, err := queue.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(queue.url), AttributeNames: []types.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attributes.Attributes["ApproximateNumberOfMessages"] != "0" || attributes.Attributes["ApproximateNumberOfMessagesNotVisible"] != "0" {
		t.Errorf("queue still has jobs: %v", attributes.Attributes)
	}
}
