package pubsub

import (
	"encoding/base64"
	"errors"
	"time"
)

const (
	nsTopics        = "pubsub/topics"
	nsSubscriptions = "pubsub/subscriptions"
)

var (
	ErrAlreadyExists = errors.New("already exists")
	ErrTopicMissing  = errors.New("topic missing")
	ErrSubMissing    = errors.New("subscription missing")
)

type topicResource struct {
	Name string `json:"name"`
}

type subscriptionResource struct {
	Name               string      `json:"name"`
	Topic              string      `json:"topic"`
	AckDeadlineSeconds int         `json:"ackDeadlineSeconds"`
	PushConfig         *pushConfig `json:"pushConfig,omitempty"`
}

type pushConfig struct {
	PushEndpoint string `json:"pushEndpoint"`
}

type publishRequest struct {
	Messages []pubsubMessage `json:"messages"`
}

type pubsubMessage struct {
	Data        string            `json:"data,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	MessageID   string            `json:"messageId,omitempty"`
	PublishTime time.Time         `json:"publishTime,omitempty"`
	OrderingKey string            `json:"orderingKey,omitempty"`
}

type publishResponse struct {
	MessageIDs []string `json:"messageIds"`
}

type pullRequest struct {
	ReturnImmediately bool `json:"returnImmediately"`
	MaxMessages       int  `json:"maxMessages"`
}

type pullResponse struct {
	ReceivedMessages []receivedMessage `json:"receivedMessages"`
}

type receivedMessage struct {
	AckID   string        `json:"ackId"`
	Message pubsubMessage `json:"message"`
}

type ackRequest struct {
	AckIDs []string `json:"ackIds"`
}

type storedMessage struct {
	AckID    string        `json:"ackId"`
	Message  pubsubMessage `json:"message"`
	Inflight bool          `json:"-"`
	Deadline time.Time     `json:"-"`
}

func (m *storedMessage) available(now time.Time) bool {
	return !m.Inflight || now.After(m.Deadline)
}

// DecodeData is exposed for tests; pub/sub message bodies are base64 in the REST API.
func DecodeData(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// pushEnvelope mirrors the real Pub/Sub push payload:
//
//	{"message": {<PubsubMessage>}, "subscription": "<full sub name>"}
//
// see https://cloud.google.com/pubsub/docs/push#receive_push.
type pushEnvelope struct {
	Message      pubsubMessage `json:"message"`
	Subscription string        `json:"subscription"`
}
