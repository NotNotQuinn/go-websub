package websub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tomnomnom/linkheader"
)

// TODO: refresh expiring subscriptions

var (
	// topic not discoverable
	ErrTopicNotDiscoverable = errors.New("topic not discoverable")
	// hub returned an invalid status code on subscription request
	ErrNon2xxOnSubReq = errors.New("hub returned an invalid status code on subscription request")
)

// an SSubscription is a subscription in the context of a Subscriber.
type SSubscription struct {
	Topic              string
	Hub                string
	Secret             string
	Expires            time.Time
	Id                 string
	callback           SubscribeCallback
	pendingSubscribe   bool
	pendingUnsubscribe bool
}

type Subscriber struct {
	// maps subscription id to subscription
	subscriptions map[string]*SSubscription
	baseUrl       string
	leaseLength   time.Duration
}

func (s Subscriber) BaseUrl() string {
	return s.baseUrl
}

func NewSubscriber(baseUrl string, options ...SubscriberOption) *Subscriber {
	s := &Subscriber{
		subscriptions: make(map[string]*SSubscription),
		baseUrl:       strings.TrimRight(baseUrl, "/"),
		leaseLength:   time.Hour * 24 * 10,
	}

	for _, opt := range options {
		opt(s)
	}

	return s
}

func (s *Subscriber) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subId := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodGet:
		sub, ok := s.subscriptions[subId]
		if !ok || sub == nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("subscription not found"))
			return
		}

		q := r.URL.Query()

		if q.Get("hub.topic") == "" {
			// possible fake request
			// the spec requires hub.topic to be sent by the hub

			w.WriteHeader(400)
			w.Write([]byte("missing 'hub.topic' query parameter"))
			return
		} else if q.Get("hub.topic") != sub.Topic {
			// doesnt match
			w.WriteHeader(404)
			w.Write([]byte("'hub.topic' query parameter does not match internal"))
			return
		}

		switch q.Get("hub.mode") {
		case "denied":
			// the hub denied a subscription request
			delete(s.subscriptions, subId)
			w.WriteHeader(http.StatusOK)

			log.Error().
				Str("topic", q.Get("hub.topic")).
				Str("reason", q.Get("hub.reason")).
				Msg("Subscription denied")

			return
		case "subscribe":
			// the hub accepted a subscribe request
			if !sub.pendingSubscribe {
				w.WriteHeader(404)
				w.Write([]byte("not pending subscription"))
				return
			}

			seconds, err := strconv.Atoi(q.Get("hub.lease_seconds"))
			if err != nil {
				// The hub will take a 5xx to mean verification failed,
				// so remove this subscription
				delete(s.subscriptions, subId)

				log.Err(err).
					Str("msg", "could not convert 'hub.lease_seconds' from string to int").
					Msg("subscription cancelled")
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			sub.Expires = time.Now().Add(time.Duration(seconds) * time.Second)
			sub.pendingSubscribe = false

			w.WriteHeader(200)
			w.Write([]byte(q.Get("hub.challenge")))
			return

		case "unsubscribe":
			// the hub accepted an usubscribe request
			if !sub.pendingUnsubscribe {
				w.WriteHeader(404)
				w.Write([]byte("not pending unsubscription"))
				return
			}

			delete(s.subscriptions, subId)
			sub.pendingUnsubscribe = false

			w.WriteHeader(200)
			w.Write([]byte(q.Get("hub.challenge")))
			return

		default:
			w.WriteHeader(400)
			w.Write([]byte("missing 'hub.mode' query parameter"))
			return
		}
	case http.MethodPost:
		sub, ok := s.subscriptions[subId]
		if !ok || sub == nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("subscription not found"))
			return
		}

		w.WriteHeader(200)

		content, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		contentReader := bytes.NewReader(content)

		signature := r.Header.Get("X-Hub-Signature")

		if sub.Secret != "" {
			if signature == "" {
				return
			}

			split := strings.Split(signature, "=")
			if len(split) < 2 {
				// invalid signature format
				return
			}

			hashFunction, hash := split[0], split[1]
			realHash, _ := calculateHash(hashFunction, sub.Secret, content)

			if hash != realHash {
				// invalid signature
				return
			}

			sub.callback(sub, r.Header.Get("Content-Type"), contentReader)
		} else {
			// no secret or signature
			sub.callback(sub, r.Header.Get("Content-Type"), contentReader)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}
}

type SubscriberOption func(*Subscriber)

// SWithBaseUrl sets the baseUrl for a subscriber
func SWithBaseUrl(baseUrl string) SubscriberOption {
	return func(s *Subscriber) {
		s.baseUrl = strings.TrimRight(baseUrl, "/")
	}
}

// SWithLeaseLength sets the LeaseLength for a subscriber
//
// Default lease length is 10 days
func SWithLeaseLength(LeaseLength time.Duration) SubscriberOption {
	return func(s *Subscriber) {
		s.leaseLength = LeaseLength
	}
}

// a SubscribeCallback is called when a subscriber receives a publish to the related topic.
type SubscribeCallback func(sub *SSubscription, contentType string, body io.Reader)

// subscribes to updates to the topicUrl, verifying using the secret
//
// If the secret is an empty string, it is omited.
//
// When updates happen, the callback is called.
func (s *Subscriber) Subscribe(topicUrl, secret string, callback SubscribeCallback) (*SSubscription, error) {
	self, hub, err := s.discover(topicUrl)

	if err != nil {
		return nil, err
	}

	sub := &SSubscription{
		Topic:            self,
		Hub:              hub,
		Expires:          time.Now().Add(s.leaseLength),
		Id:               uuid.New().String(),
		Secret:           secret,
		callback:         callback,
		pendingSubscribe: true,
	}

	s.subscriptions[sub.Id] = sub

	err = s.sendRequest(sub, "subscribe")

	if err != nil {
		return nil, err
	}

	return sub, nil
}

// Unsubscribe requests the hub to stop sending updates.
//
// All events received in the meantime will still be fulfulled.
func (s *Subscriber) Unsubscribe(sub *SSubscription) error {
	sub.pendingUnsubscribe = true
	return s.sendRequest(sub, "unsubscribe")
}

func (s *Subscriber) sendRequest(sub *SSubscription, mode string) error {
	vals := url.Values{
		"hub.mode":          []string{mode},
		"hub.topic":         []string{sub.Topic},
		"hub.callback":      []string{s.baseUrl + "/" + sub.Id},
		"hub.lease_seconds": []string{fmt.Sprint(int(s.leaseLength.Seconds()))},
	}

	if sub.Secret != "" {
		vals.Set("hub.secret", sub.Secret)
	}

	body := vals.Encode()

	resp, err := http.Post(
		sub.Hub,
		"application/x-www-form-urlencoded",
		strings.NewReader(body),
	)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Not in OK range

		receivedBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		log.Err(ErrNon2xxOnSubReq).
			Str("status", resp.Status).
			Str("body-sent", body).
			Bytes("body-received", receivedBody).
			Msg(ErrNon2xxOnSubReq.Error())
		return ErrNon2xxOnSubReq
	}
	io.Copy(io.Discard, resp.Body)

	return nil
}

// discover makes a GET request to the URL and checks link headers for "self", and "hub".
//
// Returns ErrTopicNotDiscoverable if either link is missing.
func (Subscriber) discover(topic string) (self string, hub string, err error) {
	resp, err := http.Get(topic)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic-url", topic).
			Msg("could not GET topic url")
		return
	}

	defer resp.Body.Close()

	// check for link headers
	links := linkheader.ParseMultiple(resp.Header.Values("Link"))

	selfs := links.FilterByRel("self")
	if len(selfs) > 0 {
		self = selfs[0].URL
	}

	hubs := links.FilterByRel("hub")
	if len(hubs) > 0 {
		hub = hubs[0].URL
	}

	if self != "" && hub != "" {
		return
	}

	return "", "", ErrTopicNotDiscoverable
}
