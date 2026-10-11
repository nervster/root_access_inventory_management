package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// Queue carries jobs from Relay to Worker: SQS, or a fake in tests.
type Queue interface {
	// Send adds up to 10 jobs.
	Send(ctx context.Context, jobs []Job) error
	// Receive waits up to 20 seconds for up to 10 jobs. A received job is hidden from other
	// workers until it's Done, or until RetryLater's wait (or the queue's timeout) ends.
	Receive(ctx context.Context) ([]Delivery, error)
	// Done removes a finished job.
	Done(ctx context.Context, d Delivery) error
	// RetryLater makes a failed job come back after delay.
	RetryLater(ctx context.Context, d Delivery, delay time.Duration) error
}

// Delivery is a received job and the receipt needed to finish it.
type Delivery struct {
	Job     Job
	Receipt string
}

// maxAttempts is how often a job runs before SQS moves it to the dead-letter queue.
const maxAttempts = 5

// visibilityTimeout is how long a received job stays hidden by default: longer than a handler may
// run (handlerTimeout), so a slow job isn't handed to a second worker meanwhile.
const visibilityTimeout = 2 * time.Minute

type SQSConfig struct {
	Queue    string // the queue's name, e.g. plant-stock-jobs
	Region   string
	Endpoint string // set for LocalStack (http://localhost:4566); empty means real AWS SQS
}

// Local reports whether this is LocalStack rather than real SQS.
func (c SQSConfig) Local() bool { return c.Endpoint != "" }

type SQS struct {
	client *sqs.Client
	cfg    SQSConfig
	url    string
}

// NewSQS connects to the queue. Locally it creates the queue (and its dead-letter queue) first;
// when deployed they're made with the rest of the infrastructure.
func NewSQS(ctx context.Context, cfg SQSConfig) (*SQS, error) {
	options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Local() {
		// LocalStack accepts any credentials; real SQS uses the environment's (e.g. the pod's IAM role).
		options = append(options, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	q := &SQS{cfg: cfg, client: sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})}

	if cfg.Local() {
		q.url, err = q.createLocalQueues(ctx)
	} else {
		var found *sqs.GetQueueUrlOutput
		found, err = q.client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(cfg.Queue)})
		if found != nil {
			q.url = aws.ToString(found.QueueUrl)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("SQS queue %s: %w", cfg.Queue, err)
	}
	return q, nil
}

// createLocalQueues makes the queue and its dead-letter queue (<name>-dead) in LocalStack, as
// production has them. Creating an existing queue with the same settings is fine.
func (q *SQS) createLocalQueues(ctx context.Context) (string, error) {
	dead, err := q.client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String(q.cfg.Queue + "-dead"),
		Attributes: map[string]string{"MessageRetentionPeriod": strconv.Itoa(14 * 24 * 3600)}, // the most SQS keeps
	})
	if err != nil {
		return "", err
	}
	attributes, err := q.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: dead.QueueUrl, AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return "", err
	}
	redrive, _ := json.Marshal(map[string]string{
		"deadLetterTargetArn": attributes.Attributes[string(types.QueueAttributeNameQueueArn)],
		"maxReceiveCount":     strconv.Itoa(maxAttempts),
	})
	main, err := q.client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(q.cfg.Queue),
		Attributes: map[string]string{
			string(types.QueueAttributeNameVisibilityTimeout): strconv.Itoa(int(visibilityTimeout.Seconds())),
			string(types.QueueAttributeNameRedrivePolicy):     string(redrive),
		},
	})
	if err != nil {
		return "", err
	}
	return aws.ToString(main.QueueUrl), nil
}

func (q *SQS) Send(ctx context.Context, jobs []Job) error {
	entries := make([]types.SendMessageBatchRequestEntry, len(jobs))
	for i, job := range jobs {
		body, err := json.Marshal(job)
		if err != nil {
			return err
		}
		entries[i] = types.SendMessageBatchRequestEntry{Id: aws.String(strconv.FormatInt(job.ID, 10)), MessageBody: aws.String(string(body))}
	}
	output, err := q.client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{QueueUrl: aws.String(q.url), Entries: entries})
	if err != nil {
		return fmt.Errorf("send jobs: %w", err)
	}
	if len(output.Failed) > 0 {
		// The whole batch is sent again later; the ones that made it run twice, which is safe.
		return fmt.Errorf("send jobs: %d of %d failed (first: %s)", len(output.Failed), len(jobs), aws.ToString(output.Failed[0].Message))
	}
	return nil
}

func (q *SQS) Receive(ctx context.Context) ([]Delivery, error) {
	output, err := q.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:                    aws.String(q.url),
		MaxNumberOfMessages:         10,
		WaitTimeSeconds:             20, // long polling: wait for jobs instead of asking over and over
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount},
	})
	if err != nil {
		return nil, fmt.Errorf("receive jobs: %w", err)
	}
	deliveries := make([]Delivery, 0, len(output.Messages))
	var badMessages error
	for _, message := range output.Messages {
		var job Job
		if err := json.Unmarshal([]byte(aws.ToString(message.Body)), &job); err != nil {
			// Not a job (nothing else should be on this queue); it'll end up in the dead-letter queue.
			badMessages = errors.Join(badMessages, fmt.Errorf("message %s isn't a job: %w", aws.ToString(message.MessageId), err))
			continue
		}
		job.Attempt, _ = strconv.Atoi(message.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)])
		deliveries = append(deliveries, Delivery{Job: job, Receipt: aws.ToString(message.ReceiptHandle)})
	}
	return deliveries, badMessages
}

func (q *SQS) Done(ctx context.Context, d Delivery) error {
	_, err := q.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{QueueUrl: aws.String(q.url), ReceiptHandle: aws.String(d.Receipt)})
	return err
}

func (q *SQS) RetryLater(ctx context.Context, d Delivery, delay time.Duration) error {
	_, err := q.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(q.url), ReceiptHandle: aws.String(d.Receipt), VisibilityTimeout: int32(delay.Seconds()),
	})
	return err
}
