package websub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tomnomnom/linkheader"
)

var (
	// a non 2xx status code was returned when getting topic content
	ErrNon2xxGettingContent = errors.New(
		"a non 2xx status code was returned when getting topic content")
	// a non 2xx status code was returned when posting content to a subscriber
	ErrNon2xxPostingContent = errors.New(
		"a non 2xx status code was returned when posting content to a subscriber")
)

// A Hub is a websub hub.
type Hub struct {
	// See websub.HubAllowPostBodyAsContent
	allowPostBodyAsContent bool
	// expose topics to /topics
	exposeTopics bool
	// The hub url that specifies this hub.
	hubUrl string
	// The user-agent to send on all http requests this hub makes.
	userAgent string
	// The hash function used to sign content distribution requests
	//
	// One of "sha1", "sha256", "sha384", or "sha512".
	hashFunction string
	// The number of times to retry failed POST requests to subscribers.
	retryLimit int
	// The amount of time to wait between each failed POST request to subscribers.
	retryInterval time.Duration
	// The max amount of time a subscription can be.
	maxLease time.Duration
	// The minimum amount of time a subscription can be.
	minLease time.Duration
	// The default amount of time a subscription is, if the subscriber doesn't specify.
	defaultLease time.Duration
	// The interval to clear expired subscriptions from the memory.
	cleanupInterval time.Duration
	// maps topic and callback to subscription, in that order.
	subscriptions map[string]map[string]*HubSubscription
	// Mutex lock for subscriptions map
	mu *sync.RWMutex
	// Validators that validate each incoming subscription request.
	validators []*HubSubValidatorFunc
	// Sniffers that intercept publishes as if they were subscribed to a topic.
	sniffers map[string][]*HubTopicSnifferFunc
	// All failed publishes are sent through this channel.
	failedPublishes chan *hubPublish
	// Newly found topics are sent through this channel
	// Used for publishing topic updates when h.exposeTopics is true.
	newTopic chan string
}

// represents a single POST request to make to a subscriber
type hubPublish struct {
	// The HTTP callback to the subscriber
	callback string
	// The topic URL that was updated
	topic string
	// The secret to sign the message with. Empty string means none.
	secret string
	// The content that was published.
	content []byte
	// The Content-Type of the content
	contentType string
	// The number of times this POST request has failed.
	failedCount int
}

// an HubSubscription is a subscription used in the context of a Hub.
type HubSubscription struct {
	// The HTTP callback to the subscriber.
	Callback string
	// The topic URL the subscriber is subscribing to.
	Topic string
	// The number of seconds the subscription will be active.
	LeaseLength int
	// The date/time the subscription will expire.
	// Is not set at validation time.
	Expires time.Time
	// The secret provided by the subscriber. Empty string means none.
	Secret string
}

func (h Hub) HubUrl() string {
	return h.hubUrl
}

// NewHub creates a new hub with the specified options and starts background goroutines.
func NewHub(hubUrl string, options ...HubOption) *Hub {
	h := &Hub{
		hubUrl:          strings.TrimRight(hubUrl, "/"),
		userAgent:       "go-websub-hub",
		hashFunction:    "sha1",
		retryInterval:   time.Minute,
		retryLimit:      5,
		maxLease:        time.Hour * 24 * 30,
		minLease:        time.Minute * 5,
		defaultLease:    time.Hour * 24 * 10,
		cleanupInterval: time.Minute,
		failedPublishes: make(chan *hubPublish),
		newTopic:        make(chan string),
		subscriptions:   make(map[string]map[string]*HubSubscription),
		mu:              &sync.RWMutex{},
		sniffers:        make(map[string][]*HubTopicSnifferFunc),
	}

	for _, opt := range options {
		opt(h)
	}

	go h.handleFailedPublishes()
	go h.removeExpiredSubscriptions(h.cleanupInterval)
	if h.exposeTopics {
		go h.publishTopicUpdates()
	} else {
		go h.consumeTopicUpdates()
	}

	return h
}

// A HubOption specifies an option for a hub.
type HubOption func(*Hub)

// HubWithCleanupInterval sets the interval expired subscriptions are removed from the memory
func HubWithCleanupInterval(interval time.Duration) HubOption {
	return func(h *Hub) {
		h.cleanupInterval = interval
	}
}

// HubWithLeaseSettings sets the minimum, maximum, and default lease for a subscription on a hub
//
// When a requested lease is outside of the allowed range, the lease becomes pinned
// to the minimum or maximum value. If a subscriber doesn't provide a lease length,
// the default lease length is used.
//
// - Default minimum lease is 5 minutes
//
// - Default maximum lease is 720 hours (30 days)
//
// - Default default lease is 240 hours (10 days)
func HubWithLeaseSettings(minLease, maxLease, defaultLease time.Duration) HubOption {
	return func(h *Hub) {
		h.minLease = minLease
		h.maxLease = maxLease
		h.defaultLease = defaultLease
	}
}

// HubExposeTopics enables a /topics endpoint that lists all available/active topics.
func HubExposeTopics(enable bool) HubOption {
	return func(h *Hub) {
		h.exposeTopics = enable
	}
}

// HubAllowPostBodyAsContent allows publishers to post content as the body
// of the POST request if they provide hub.content = "body" and hub.mode = "publish".
// In this case, the Content-Type of the post request is used when distributing publish events.
//
// NOTE: Because of the lack of authentication for publishers, this allows
// any machine with internet access to the hub to publish any content
// under any topic. Use with caution.
func HubAllowPostBodyAsContent(enable bool) HubOption {
	return func(h *Hub) {
		h.allowPostBodyAsContent = enable
	}
}

// HubWithUserAgent sets the user-agent for a hub
//
// Default user agent is "go-websub-hub"
func HubWithUserAgent(userAgent string) HubOption {
	return func(h *Hub) {
		h.userAgent = userAgent
	}
}

// HubWithHashFunction sets the hash function used to compute hub signatures
// for subscriptions with a secret.
//
// One of "sha1", "sha256", "sha384", "sha512"
//
// Default is "sha1" for compatability, however it is insecure.
func HubWithHashFunction(hashFunction string) HubOption {
	return func(h *Hub) {
		h.hashFunction = hashFunction
	}
}

// HubWithRetryLimits sets the retry limits for a hub.
//
// Defaults to 5 retries, and a one minute interval.
func HubWithRetryLimits(retryLimit int, retryInterval time.Duration) HubOption {
	return func(h *Hub) {
		h.retryLimit = retryLimit
		h.retryInterval = retryInterval
	}
}

// Handles incoming HTTP requests
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.URL.Path == "/topics" && h.exposeTopics {
		h.getTopicsHTTP(w, r)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "couldnt not read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var useBody = true
	var q url.Values = r.URL.Query()
	if !h.allowPostBodyAsContent || q.Get("hub.content") != "body" {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			http.Error(w,
				"invalid Content-Type; should be \"application/x-www-form-urlencoded\"", 400)
			return
		}

		useBody = false
		// combine query into q
		q, err = url.ParseQuery(strings.TrimPrefix(string(body)+"&"+q.Encode(), "&"))

		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}

	if q.Get("hub.mode") == "" {
		w.WriteHeader(400)
		w.Write([]byte("'hub.mode' missing or empty"))
		return
	}

	switch q.Get("hub.mode") {
	case "publish":
		topic := q.Get("hub.topic")
		if topic == "" {
			topic = q.Get("hub.url")
		}

		if topic == "" {
			w.WriteHeader(400)
			w.Write([]byte("missing 'hub.topic' or 'hub.url'"))
			return
		}

		var content []byte
		var contentType string

		if useBody {
			content = body
			contentType = r.Header.Get("Content-Type")
		} else {
			content, contentType, err = h.getTopicContent(topic)

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(200)
		h.Publish(topic, contentType, content)
		return

	case "unsubscribe":
		fallthrough
	case "subscribe":
		unsubscribe := q.Get("hub.mode") == "unsubscribe"
		leaseLength := int(h.defaultLease.Seconds())
		if q.Get("hub.lease_length") != "" {
			leaseLength, err = strconv.Atoi(q.Get("hub.lease_seconds"))

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		if leaseLength > int(h.maxLease.Seconds()) {
			leaseLength = int(h.maxLease.Seconds())
		}

		if leaseLength < int(h.minLease.Seconds()) {
			leaseLength = int(h.minLease.Seconds())
		}

		var sub *HubSubscription
		update := false

		topic := q.Get("hub.topic")
		callback := q.Get("hub.callback")

		h.mu.RLock()
		cond := h.subscriptions[topic] != nil
		h.mu.RLock()
		if cond {
			h.mu.Lock()
			sub = h.subscriptions[topic][callback]
			h.mu.Unlock()
		}

		if sub == nil && unsubscribe {
			w.WriteHeader(404)
			w.Write([]byte("subscription not found"))
			return
		}

		if sub == nil {
			sub = &HubSubscription{
				Callback:    callback,
				Topic:       topic,
				Secret:      q.Get("hub.secret"),
				LeaseLength: leaseLength,
			}
		} else {
			update = true
		}

		if sub.Callback == "" {
			w.WriteHeader(400)
			w.Write([]byte("'hub.callback' missing or empty"))
			return
		}

		if sub.Topic == "" {
			w.WriteHeader(400)
			w.Write([]byte("'hub.topic' missing or empty"))
			return
		}

		if len([]byte(sub.Secret)) > 200 {
			w.WriteHeader(400)
			w.Write([]byte("'hub.secret' must be less than 200 bytes"))
			return
		}

		w.WriteHeader(202)
		go func() {
			if !unsubscribe {
				ok, reason := h.validateSubscription(sub)

				if !ok {
					err = h.sendValidationDenied(sub, reason)
					log.Err(err).Msg("could not deny subscription")
					return
				}
			}

			ok, err := h.verifyIntent(sub, "subscribe")
			if err != nil {
				log.Err(err).Msg("could not verify (un)subscription")
				return
			}

			if ok {
				if unsubscribe {
					h.mu.Lock()
					delete(h.subscriptions[sub.Topic], sub.Callback)
					h.mu.Unlock()
				} else {
					sub.Expires = time.Now().Add(time.Duration(leaseLength) * time.Second)
					if update {
						sub.LeaseLength = leaseLength
						sub.Secret = q.Get("hub.secret")
					} else {
						h.mu.RLock()
						cond := h.subscriptions[sub.Topic] == nil
						h.mu.RUnlock()
						if cond {
							h.mu.Lock()
							h.subscriptions[sub.Topic] = make(map[string]*HubSubscription)
							h.mu.Unlock()
							h.newTopic <- sub.Topic
						}
						h.mu.Lock()
						h.subscriptions[sub.Topic][sub.Callback] = sub
						h.mu.Unlock()
					}
				}
			}
		}()

	default:
		w.WriteHeader(400)
		w.Write([]byte("'hub.mode' not recognized"))
		return
	}
}

// HubSubValidatorFunc validates subscription requests.
//
// The validation stops as soon as one validator returns ok=false. The provided
// reason is sent to the subscriber telling them their subscription request was denied.
//
// The expiry date will not be set by the time the validators are called.
type HubSubValidatorFunc func(sub *HubSubscription) (ok bool, reason string)

// AddValidator adds a validator for subscription requests.
// Multiple validators can exist on one hub.
//
// All subscriptions are accepted by default.
func (h *Hub) AddValidator(validator HubSubValidatorFunc) {
	h.validators = append(h.validators, &validator)
}

// checks all validators associated with the hub for the subscription.
//
// The validation stops as soon as one validator returns ok=false.
func (h *Hub) validateSubscription(sub *HubSubscription) (ok bool, reason string) {
	for _, validator := range h.validators {
		ok, reason := (*validator)(sub)
		if !ok {
			return ok, reason
		}
	}

	return true, ""
}

// sends a GET request to the subscription callback, telling the subscriber they were denied.
func (h *Hub) sendValidationDenied(sub *HubSubscription, reason string) error {
	callback, err := url.Parse(sub.Callback)
	if err != nil {
		return err
	}

	q := callback.Query()
	q.Add("hub.mode", "denied")
	q.Add("hub.topic", sub.Topic)
	q.Add("hub.reason", reason)

	callback.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", callback.String(), nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", h.userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return nil
}

// Sends a GET request to the subscription callback, verifying if the subscriber wants to
// subscribe or unsubscribe from a subscription.
func (h *Hub) verifyIntent(sub *HubSubscription, mode string) (ok bool, err error) {
	callback, err := url.Parse(sub.Callback)
	if err != nil {
		return false, err
	}

	challenge := uuid.NewString()

	q := callback.Query()
	q.Add("hub.mode", mode)
	q.Add("hub.topic", sub.Topic)
	q.Add("hub.challenge", challenge)
	q.Add("hub.lease_seconds", fmt.Sprint(sub.LeaseLength))

	callback.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", callback.String(), nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("User-Agent", h.userAgent)
	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		return false, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	if string(body) != challenge {
		return false, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Non 2xx
		return false, nil
	}

	return true, nil
}

// A topic sniffer sniffs on topics as if it was a subscriber.
type HubTopicSnifferFunc func(topic string, contentType string, body io.Reader)

// AddSniffer allows one to "sniff" publishes, receiving events
// as if they were subscribers.
//
// If an emptry string is provided as the topic, all publishes are sniffed.
func (h *Hub) AddSniffer(topic string, sniffer HubTopicSnifferFunc) {
	h.sniffers[topic] = append(h.sniffers[topic], &sniffer)
}

// Publish publishes a topic with the specified content and content-type.
func (h *Hub) Publish(topic, contentType string, content []byte) {
	h.mu.RLock()
	cond := h.subscriptions[topic] == nil
	h.mu.RUnlock()
	if cond {
		h.mu.Lock()
		h.subscriptions[topic] = make(map[string]*HubSubscription)
		h.mu.Unlock()
		h.newTopic <- topic
	}

	// call all sniffers for this topic, even if no subscriptions exist.
	go func() {
		for sniffedTopic, sniffers := range h.sniffers {
			if sniffedTopic == "" || sniffedTopic == topic {
				for _, sniffer := range sniffers {
					(*sniffer)(topic, contentType, bytes.NewReader(content))
				}
			}
		}
	}()

	h.mu.RLock()
	for _, sub := range h.subscriptions[topic] {
		if sub.Expires.After(time.Now()) {

			go func(sub *HubSubscription) {
				pub := &hubPublish{
					callback:    sub.Callback,
					topic:       topic,
					secret:      sub.Secret,
					content:     content,
					contentType: contentType,
				}
				err := h.disbatchPublish(pub)
				if err != nil {
					log.Err(err).
						Str("callback", pub.callback).
						Str("topic", pub.topic).
						Str("contentType", pub.contentType).
						Msg("could not disbatch publish")

					pub.failedCount++
					h.failedPublishes <- pub
				}
			}(sub)
		}
	}
	h.mu.RUnlock()
}

// disbatchPublish sends a publish request (POST) for the publish object.
func (h *Hub) disbatchPublish(pub *hubPublish) error {
	req, err := http.NewRequest("POST", pub.callback, bytes.NewReader(pub.content))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", pub.contentType)
	req.Header.Set("Link", linkheader.Links{
		{
			Rel: "self",
			URL: pub.topic,
		},
		{
			Rel: "hub",
			URL: h.hubUrl,
		},
	}.String())

	if pub.secret != "" {
		req.Header.Set("X-Hub-Signature", h.newSignature(pub.content, pub.secret))
	}

	req.Header.Set("User-Agent", h.userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrNon2xxPostingContent
	}

	return nil
}

// newSignature generates an X-Hub-Signature header
func (h *Hub) newSignature(content []byte, secret string) string {
	hash, hashName := calculateHash(h.hashFunction, secret, content)

	return hashName + "=" + hash
}

// gets the topic content from the topic URL, via GET request.
func (h *Hub) getTopicContent(topic string) (content []byte, contentType string, err error) {
	req, err := http.NewRequest("GET", topic, nil)
	if err != nil {
		return nil, "", err
	}

	req.Header.Set("User-Agent", h.userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Non 2xx
		return nil, "", ErrNon2xxGettingContent
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return bytes, resp.Header.Get("Content-Type"), nil
}

// retry failed publishes based on retryInterval and retryLimit
func (h *Hub) handleFailedPublishes() {
	for {
		pub := <-h.failedPublishes
		go func(pub *hubPublish) {
			<-time.After(h.retryInterval)

			err := h.disbatchPublish(pub)
			if err != nil {
				log.Err(err).
					Str("callback", pub.callback).
					Str("topic", pub.topic).
					Str("contentType", pub.contentType).
					Msg("could not disbatch publish")

				pub.failedCount++

				if h.retryLimit >= pub.failedCount {
					h.failedPublishes <- pub
				}
			}

		}(pub)
	}
}

// periodically removes expired subscriptions from the memory.
func (h *Hub) removeExpiredSubscriptions(interval time.Duration) {
	t := time.NewTicker(interval)
	for {
		<-t.C

		var deleteMe []struct {
			topic, callback string
		}

		h.mu.RLock()
		for topic, subs := range h.subscriptions {
			for callback, sub := range subs {
				if !sub.Expires.After(time.Now()) {
					deleteMe = append(deleteMe, struct {
						topic, callback string
					}{
						topic:    topic,
						callback: callback,
					})
				}
			}
		}
		h.mu.RUnlock()

		if len(deleteMe) > 0 {
			h.mu.Lock()
			for _, v := range deleteMe {
				delete(h.subscriptions[v.topic], v.callback)
			}
			h.mu.Unlock()
		}
	}
}

// Returns an array of all topics.
//
// Includes topics with no subscribers, or no publishes.
func (h *Hub) GetTopics() (topics []string) {
	h.mu.RLock()
	topics = make([]string, 0, len(h.subscriptions))
	for topic := range h.subscriptions {
		topics = append(topics, topic)
	}
	h.mu.RUnlock()

	return
}

// Handles requests to /topics when h.exposeTopics is enabled.
func (h *Hub) getTopicsHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	topics := h.GetTopics()

	bytes, err := json.Marshal(topics)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Link", linkheader.Links{
		{
			Rel: "self",
			URL: h.hubUrl + "/topics",
		},
		{
			Rel: "hub",
			URL: h.hubUrl + "/",
		}}.String())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(bytes)
	return
}

func (h *Hub) publishTopicUpdates() {
	for range h.newTopic {
		bytes, err := json.Marshal(h.GetTopics())
		if err != nil {
			log.Err(err).Msg("could not marshal topic json on publish")
			return
		}

		// Must be run in goroutine, otherwise there is deadlock
		// on the first publish to this topic because h.newTopic
		// is unbuffered and h.Publish sends a new message
		go h.Publish(h.hubUrl+"/topics", "application/json", bytes)
	}
}

func (h *Hub) consumeTopicUpdates() {
	for range h.newTopic {
		//	do nothing
	}
}
