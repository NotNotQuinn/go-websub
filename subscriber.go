package websub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tomnomnom/linkheader"
	"golang.org/x/net/html"
)

// TODO: refresh expiring subscriptions
// TODO: expose a list of all subscriptions via go API or something
//       so people can unsubscribe without needing to keep track of their
//       own subscriptions

var (
	// topic not discoverable
	ErrTopicNotDiscoverable = errors.New("topic not discoverable")
	// hub returned an invalid status code on subscription or unsubscription request
	ErrNon2xxOnSubReq = errors.New("hub returned an invalid status code on subscription or unsubscription request")
)

// SubscriberSubscription is a subscription in the context of a Subscriber.
type SubscriberSubscription struct {
	// Topic URL for this subscription.
	// Not always equal to the passed topic url.
	Topic string
	// Hub URL this subscription is from.
	Hub string
	// Secret string used for verifying the hub is the
	// sender of the subscription.
	Secret string
	// The date/time this subscription expires.
	Expires time.Time
	// Internal ID for this subscription. Part of the callback URL.
	ID string
	// Callback function to be invoked when a publish is received.
	callback SubscribeCallback
	// Whether this subscription is pending a subscription verification
	pendingSubscribe bool
	// Whether this subscription is pending an unsubscription verification
	pendingUnsubscribe bool
}

type Subscriber struct {
	// Maps subscription id to subscription
	subscriptions map[string]*SubscriberSubscription
	// subscriptions map guard
	mu *sync.RWMutex
	// Base URL for this subscribers callback URLs.
	baseURL string
	// Lease length used for all subscriptions. Default 240 hours.
	leaseLength time.Duration
}

// NewSubscriber creates a new subscriber with the specified options.
func NewSubscriber(baseURL string, options ...SubscriberOption) *Subscriber {
	s := &Subscriber{
		subscriptions: make(map[string]*SubscriberSubscription),
		mu:            &sync.RWMutex{},
		baseURL:       strings.TrimRight(baseURL, "/"),
		leaseLength:   time.Hour * 24 * 10,
	}

	for _, opt := range options {
		opt(s)
	}

	return s
}

// ServeHTTP handles all incoming HTTP requests, following websub spec.
func (s *Subscriber) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	subID := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodGet:
		s.mu.RLock()
		sub, ok := s.subscriptions[subID]
		s.mu.RUnlock()
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
			s.mu.Lock()
			delete(s.subscriptions, subID)
			s.mu.Unlock()
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
				s.mu.Lock()
				delete(s.subscriptions, subID)
				s.mu.Unlock()

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
			// the hub accepted an unsubscribed request
			if !sub.pendingUnsubscribe {
				w.WriteHeader(404)
				w.Write([]byte("not pending unsubscription"))
				return
			}

			s.mu.Lock()
			delete(s.subscriptions, subID)
			s.mu.Unlock()
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
		s.mu.RLock()
		sub, ok := s.subscriptions[subID]
		s.mu.RUnlock()
		if !ok || sub == nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("subscription not found"))
			return
		}

		w.WriteHeader(200)

		if sub.pendingUnsubscribe {
			return
		}

		content, err := io.ReadAll(r.Body)
		if err != nil {
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

			go sub.callback(sub, r.Header.Get("Content-Type"), contentReader)
		} else {
			// no secret
			go sub.callback(sub, r.Header.Get("Content-Type"), contentReader)
		}

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}
}

// BaseURL returns the base URL for this subscribers callback URLs.
func (s Subscriber) BaseURL() string {
	return s.baseURL
}

type SubscriberOption func(*Subscriber)

// SubscriberWithBaseURL sets the baseURL for a subscriber
func SubscriberWithBaseURL(baseURL string) SubscriberOption {
	return func(s *Subscriber) {
		s.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// SubscriberWithLeaseLength sets the LeaseLength for a subscriber
//
// Default lease length is 10 days
func SubscriberWithLeaseLength(LeaseLength time.Duration) SubscriberOption {
	return func(s *Subscriber) {
		s.leaseLength = LeaseLength
	}
}

// SubscribeCallback is called when a subscriber receives a publish to the related topic.
type SubscribeCallback func(sub *SubscriberSubscription, contentType string, body io.Reader)

// Subscribe subscribes to updates to the topicUrl, verifying using the secret
//
// If the secret is an empty string, it is omitted.
//
// When updates happen, the callback is called.
func (s *Subscriber) Subscribe(topicURL, secret string, callback SubscribeCallback) (*SubscriberSubscription, error) {
	self, hub, err := s.discover(topicURL)

	if err != nil {
		return nil, err
	}

	sub := &SubscriberSubscription{
		Topic:            self,
		Hub:              hub,
		Expires:          time.Now().Add(s.leaseLength),
		ID:               uuid.New().String(),
		Secret:           secret,
		callback:         callback,
		pendingSubscribe: true,
	}

	s.mu.RLock()
	s.subscriptions[sub.ID] = sub
	s.mu.RUnlock()

	err = s.sendRequest(sub, "subscribe")

	if err != nil {
		return nil, err
	}

	return sub, nil
}

// Unsubscribe requests the hub to stop sending updates.
// Already pending unsubscriptions are ignored.
//
// All events received in the meantime will not be fulfilled.
func (s *Subscriber) Unsubscribe(sub *SubscriberSubscription) error {
	if !sub.pendingUnsubscribe {
		sub.pendingUnsubscribe = true
		return s.sendRequest(sub, "unsubscribe")
	}

	return nil
}

func (s *Subscriber) sendRequest(sub *SubscriberSubscription, mode string) error {
	vals := url.Values{
		"hub.mode":          []string{mode},
		"hub.topic":         []string{sub.Topic},
		"hub.callback":      []string{s.baseURL + "/" + sub.ID},
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

// discover attempts to discover a topic
//
// Returns ErrTopicNotDiscoverable if links to self or hub are missing.
func (s *Subscriber) discover(topic string) (self string, hub string, err error) {

	resp, err := http.Get(topic)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic-url", topic).
			Msg("could not GET topic url")
		return
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	self, hub, err = s.discoverHTTPHeader(resp, bytes.NewReader(content))
	if err != nil && err != ErrTopicNotDiscoverable {
		return "", "", err
	}

	if self != "" && hub != "" {
		return self, hub, nil
	}

	self, hub, err = s.discoverHTTPHeader(resp, bytes.NewReader(content))
	if err != nil && err != ErrTopicNotDiscoverable {
		return "", "", err
	}

	if self != "" && hub != "" {
		return self, hub, nil
	}

	return "", "", ErrTopicNotDiscoverable
}

// discoverHTTPHeader checks link headers for "self", and "hub".
//
// Returns ErrTopicNotDiscoverable if either link is missing.
func (Subscriber) discoverHTTPHeader(resp *http.Response, body io.Reader) (self string, hub string, err error) {
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

// discoverHTMLTag checks HTML head for link tags with relations "self" and "hub".
//
// Returns ErrTopicNotDiscoverable if Content-Type is not text/html or if either was not found.
func (Subscriber) discoverHTMLTag(resp *http.Response, body io.Reader) (self string, hub string, err error) {
	tokenizer := html.NewTokenizer(body)

	if mediatype, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mediatype != "text/html" {
		return "", "", ErrTopicNotDiscoverable
	}

	inHead := false
	for {
		tokenType := tokenizer.Next()

		switch {
		case tokenType == html.ErrorToken:
			return "", "", ErrTopicNotDiscoverable

		case tokenType == html.StartTagToken:
			token := tokenizer.Token()
			if token.Data == "head" {
				inHead = true
			}

			if token.Data == "link" && inHead {
				if token.Attr[0].Key == "rel" && token.Attr[0].Val == "hub" {
					hub = token.Attr[1].Val
				}
				if token.Attr[0].Key == "rel" && token.Attr[0].Val == "self" {
					self = token.Attr[1].Val
				}
			}

		case tokenType == html.EndTagToken:
			token := tokenizer.Token()
			if token.Data == "head" {
				return self, hub, nil
			}
		}
	}
}
