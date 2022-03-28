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
	"time"

	"github.com/google/uuid"
	"github.com/tomnomnom/linkheader"
)

var (

	// a non 2xx status code was returned when getting topic content
	ErrNon2xxGettingContent = errors.New("a non 2xx status code was returned when getting topic content")
	// a non 2xx status code was returned when posting content to a subscriber
	ErrNon2xxPostingContent = errors.New("a non 2xx status code was returned when posting content to a subscriber")
)

// A Hub is a websub hub, and
type Hub struct {
	// See websub.HAllowPostBodyAsContent
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
	// The HTTP client the hub uses.
	client *http.Client
	// maps topic and callback to subscription, in that order.
	subscriptions map[string]map[string]*HSubscription
	// Validators that validate each incoming subscription request.
	validators []*HubSubscriptionValidator
	// Sniffers allow the hub to "sniff" on publishes.
	sniffers map[string][]*TopicSniffer
	// All failed publishes are sent through this channel.
	failedPublishes chan *hPublish
	// Newly found topics are sent through this channel
	newTopic chan string
}

// represents a single POST request to make to a subscriber
type hPublish struct {
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

// an HSubnscription is a subscription used in the context of a Hub.
type HSubscription struct {
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
		client:          http.DefaultClient,
		failedPublishes: make(chan *hPublish),
		newTopic:        make(chan string),
		subscriptions:   make(map[string]map[string]*HSubscription),
		sniffers:        make(map[string][]*TopicSniffer),
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

// HWithCleanupInterval sets the interval expired subscriptions are removed from the memory
func HWithCleanupInterval(interval time.Duration) HubOption {
	return func(h *Hub) {
		h.cleanupInterval = interval
	}
}

// HWithMaxLease sets the max lease for a subscription on a hub
//
// Default max lease is 720 hours (30 days)
func HWithMaxLease(d time.Duration) HubOption {
	return func(h *Hub) {
		h.maxLease = d
	}
}

// HWithMinLease sets the minimum lease for a subscription on a hub
//
// Default min lease is 5 minutes
func HWithMinLease(d time.Duration) HubOption {
	return func(h *Hub) {
		h.minLease = d
	}
}

// HWithDefaultLease sets the minimum lease for a subscription on a hub
//
// Default default lease is 240 hours (10 days)
func HWithDefaultLease(d time.Duration) HubOption {
	return func(h *Hub) {
		h.defaultLease = d
	}
}

// HExposeTopics enables a /topics endpoint that lists all availibe/active topics.
func HExposeTopics(enable bool) HubOption {
	return func(h *Hub) {
		h.exposeTopics = enable
	}
}

// HAllowPostBodyAsContent allows publishers to post content as the body
// of the POST request if they provide hub.content = "body" and hub.mode = "publish".
// In this case, the Content-Type of the post request is used when distributing publish events.
//
// NOTE: Because of the lack of authentication for publishers, this allows
// any machine with internet access to the hub to publish any content
// under any topic. Use with caution.
func HAllowPostBodyAsContent(enable bool) HubOption {
	return func(h *Hub) {
		h.allowPostBodyAsContent = enable
	}
}

// HWithUserAgent sets the user-agent for a hub
//
// Default user agent is "go-websub-hub"
func HWithUserAgent(userAgent string) HubOption {
	return func(h *Hub) {
		h.userAgent = userAgent
	}
}

// HWithHashFunction sets the hash function used to compute hub signatures
// for subscriptions with a secret.
//
// One of "sha1", "sha256", "sha384", "sha512"
//
// Default is "sha1" for compatability, however it is insecure.
func HWithHashFunction(hashFunction string) HubOption {
	return func(h *Hub) {
		h.hashFunction = hashFunction
	}
}

type TopicSniffer func(topic string, contentType string, body io.Reader)

// HWithSniffer allows one to "sniff" publishes, receiving events as if they were subscribers.
// Many sniffers can exist on one hub.
//
// If an emptry string is provided as the topic, ALL publishes are sniffed.
func HWithSniffer(topic string, sniffer TopicSniffer) HubOption {
	return func(h *Hub) {
		h.sniffers[topic] = append(h.sniffers[topic], &sniffer)
	}
}

// HubSubscriptionValidator validates subscription requests.
//
// The validation stops as soon as one validator returns ok=false. The provided
// reason is sent to the subscriber telling them their subscription request was denied.
//
// The expiry date will not be set by the time the validators are called.
type HubSubscriptionValidator func(sub *HSubscription) (ok bool, reason string)

// HWithSubscriptionValidator adds a validator for subscription requests.
// Multiple validators can exist on one hub.
//
// All subscriptions are accepted by default.
func HWithSubscriptionValidator(validator HubSubscriptionValidator) HubOption {
	return func(h *Hub) {
		h.validators = append(h.validators, &validator)
	}
}

// HWithRetryLimits sets the retry limits for a hub.
//
// Defaults to 5 retries, and a one minute interval.
func HWithRetryLimits(retryLimit int, retryInterval time.Duration) HubOption {
	return func(h *Hub) {
		h.retryLimit = retryLimit
		h.retryInterval = retryInterval
	}
}

// Handles incoming HTTP requests
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	if r.URL.Path == "/topics" && h.exposeTopics {
		h.getTopics(w, r)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cound not read request body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	var useBody = true
	var q url.Values = r.URL.Query()
	if !h.allowPostBodyAsContent || q.Get("hub.content") != "body" {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			http.Error(w, "invalid Content-Type; should be \"application/x-www-form-urlencoded\"", 400)
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

		var sub *HSubscription
		update := false

		if h.subscriptions[q.Get("hub.topic")] != nil {
			sub = h.subscriptions[q.Get("hub.topic")][q.Get("hub.callback")]
		}

		if sub == nil && unsubscribe {
			w.WriteHeader(404)
			w.Write([]byte("subscription not found"))
			return
		}

		if sub == nil {
			sub = &HSubscription{
				Callback:    q.Get("hub.callback"),
				Topic:       q.Get("hub.topic"),
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
					delete(h.subscriptions[sub.Topic], sub.Callback)
				} else {
					sub.Expires = time.Now().Add(time.Duration(leaseLength) * time.Second)
					if update {
						sub.LeaseLength = leaseLength
						sub.Secret = q.Get("hub.secret")
					} else {
						if h.subscriptions[sub.Topic] == nil {
							h.newTopic <- sub.Topic
							h.subscriptions[sub.Topic] = make(map[string]*HSubscription)
						}
						h.subscriptions[sub.Topic][sub.Callback] = sub
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

// checks all validators asociated with the hub for the subscription.
//
// The validation stops as soon as one validator returns ok=false.
func (h *Hub) validateSubscription(sub *HSubscription) (ok bool, reason string) {
	for _, validator := range h.validators {
		ok, reason := (*validator)(sub)
		if !ok {
			return ok, reason
		}
	}

	return true, ""
}

// sends a GET request to the subscription callback, telling the subscriber they were denied.
func (h *Hub) sendValidationDenied(sub *HSubscription, reason string) error {
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

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	return nil
}

// Sends a GET request to the subscription callback, verifying if the subscriber wants to
// subscribe or unsubscribe from a subscription.
func (h *Hub) verifyIntent(sub *HSubscription, mode string) (ok bool, err error) {
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

	resp, err := h.client.Do(req)

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

// Publish publishes a topic with the specified content and content-type.
func (h *Hub) Publish(topic, contentType string, content []byte) {
	if h.subscriptions[topic] == nil {
		h.newTopic <- topic
		h.subscriptions[topic] = make(map[string]*HSubscription)
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

	for _, sub := range h.subscriptions[topic] {
		if sub.Expires.After(time.Now()) {

			go func(sub *HSubscription) {
				pub := &hPublish{
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
}

// disbatchPublish sends a publish request (POST) for the publish object.
func (h *Hub) disbatchPublish(pub *hPublish) error {
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

	resp, err := h.client.Do(req)
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

	resp, err := h.client.Do(req)
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
		go func(pub *hPublish) {
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
		for topic, subs := range h.subscriptions {
			for callback, sub := range subs {
				if !sub.Expires.After(time.Now()) {
					delete(h.subscriptions[topic], callback)
				}
			}
		}
	}
}

func (h *Hub) _getTopics() ([]byte, error) {
	keys := make([]string, 0, len(h.subscriptions))
	for k := range h.subscriptions {
		keys = append(keys, k)
	}

	bytes, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	return bytes, nil
}
func (h *Hub) getTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bytes, err := h._getTopics()
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
		bytes, err := h._getTopics()
		if err != nil {
			log.Err(err).Msg("could not marshal topic json on publish")
		}

		go h.Publish(h.hubUrl+"/topics", "application/json", bytes)
	}
}

func (h *Hub) consumeTopicUpdates() {
	for range h.newTopic {
		//	do nothing
	}
}
