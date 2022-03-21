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

	"github.com/alecthomas/repr"
	"github.com/tomnomnom/linkheader"
)

// Represents the result of "discovering" a topic URL
type subscriptionTarget struct {
	// Advertised Hub URL
	HubURL string

	// Advertised "Self" topic URL (can be different from original)
	TopicURL string
}

type subscription struct {
	// When the subscription expires
	Expires time.Time

	// Callback registered to this subscription (starting with /)
	CallbackURL string

	// Topic URL requested for this subscription (dont rely on for identifying requests)
	RequestedTopic string

	// Real topic URL and Hub URL
	Target subscriptionTarget

	// The callback function is called whenever an event is received
	// for this subscription and the subscription is still active
	//
	// The body is a copy of the POST body from the hub.
	CallbackFunc *func(body io.Reader)

	// is true if subscription is pending verification for a (un)subscription from the hub
	pendingVerification bool

	// if false, the callback will not be called even if an event is received
	active bool
}

var (
	subscriptions []*subscription
	// SubscriberUrlPrefix is prepended to random data to create unique urls.
	// If SubscriberUrlPrefix is an empty string, then the URLs are "/RANDOM_DATA"
	//
	// An example URL is "/websub/s/RANDOM_DATA".
	//
	// Default: "websub/s"
	SubscriberUrlPrefix string = "websub/s"
)

// Subscribes to the provided topic
//
// The callback function gets called with the request body whenever an event is received.
func Subscribe(topic string, callback func(body io.Reader)) (*subscription, error) {
	target, err := discover(topic)
	if err != nil {
		fmt.Println(target)
		log.Error().
			Err(err).
			Str("topic-url", topic).
			Msg("could not discover topic url")
		return nil, err
	}

	url, err := randomURL(SubscriberUrlPrefix)

	if err != nil {
		return nil, err
	}

	sub := &subscription{
		Expires:             time.Now(),
		CallbackURL:         url,
		RequestedTopic:      topic,
		Target:              *target,
		CallbackFunc:        &callback,
		active:              true,
		pendingVerification: true,
	}

	err = registerNewSubscription(sub)

	if err != nil {
		return nil, err
	}

	return sub, nil
}

// Register a subscription into the memory cache and ServeMux, and request
// a subscription from the associated hub.
func registerNewSubscription(sub *subscription) error {
	if sub == nil {
		return nil
	}

	subscriptions = append(subscriptions, sub)
	serveMux.Handle(sub.CallbackURL, newSubscriptionHandler(sub))

	// Subscribe to the topic
	err := sendSubscribeRequest(sub)

	if err != nil {
		return err
	}

	return nil
}

func newSubscriptionHandler(sub *subscription) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("User-Agent", UserAgentSubscriber)
		// GET is used to verify (un)subscription requests
		// POST is used for actual events
		if r.Method == http.MethodGet {
			if !sub.pendingVerification {
				// we didnt ask for this?
				// who are you!
				repr.Println(sub)
				w.WriteHeader(404)
				w.Write([]byte("Not pending verification"))
				return
			}

			q := r.URL.Query()

			if q.Get("hub.topic") != sub.Target.TopicURL {
				w.WriteHeader(404)
				w.Write([]byte("Invalid topic URL"))
				return
			}

			if q.Get("hub.mode") == "denied" {
				// hub denied us!
				sub.active = false
				sub.pendingVerification = false
				w.WriteHeader(200)
				return
			}

			if sub.active {
				// we should be verifying a subscription
				if q.Get("hub.mode") != "subscribe" {
					w.WriteHeader(404)
					w.Write([]byte("Expected hub.mode=subscribe, found something else"))
					return
				}

				// the number of seconds before this subscription expires
				leaseSecondsStr := q.Get("hub.lease_seconds")
				leaseSeconds, err := strconv.Atoi(leaseSecondsStr)

				if err != nil {
					log.Error().Err(err).
						Str("requested-topic-url", sub.RequestedTopic).
						Str("real-topic-url", sub.Target.TopicURL).
						Str("hub-url", sub.Target.HubURL).
						Str("raw-incoming-req", r.URL.String()).
						Msg("error converting string to int (hub.lease_seconds)")
					w.WriteHeader(500) // internal server error
					// TODO: retry subscribing to subscriptions that fail in this way.
					sub.active = false
					sub.pendingVerification = false
					return
				}

				sub.Expires = time.Now().Add(time.Duration(leaseSeconds) * time.Second)
			} else {
				// we should be verifying an unsubscription
				if q.Get("hub.mode") != "unsubscribe" {
					w.WriteHeader(404)
					w.Write([]byte("Expected hub.mode=unsubscribe, found something else"))
					return
				}
			}

			// looks like a legit request, respond with the challenge
			// to verify
			w.WriteHeader(200)
			w.Write([]byte(q.Get("hub.challenge")))
			sub.pendingVerification = false

			return
		} else if r.Method == http.MethodPost {
			// An event was triggered
			buf := bytes.NewBuffer(nil)
			io.Copy(buf, r.Body)
			r.Body.Close()
			w.WriteHeader(200) // acknowledge we got the event

			if sub.CallbackFunc != nil && sub.active {
				(*sub.CallbackFunc)(buf)
			}
		} else {
			// 405: Method Not Allowed
			w.WriteHeader(405)
			w.Write([]byte("Method Not Allowed"))
		}
	})
}

// sendSubscribeRequest sends a POST request to the Hub of the subscription.
func sendSubscribeRequest(sub *subscription) error {
	if sub == nil {
		return nil
	}

	params := url.Values{}

	if BaseURL == "" {
		return errors.New("cannot send subscribe request before setting BaseURL")
	}

	fullCallback := strings.TrimRight(BaseURL, "/") + sub.CallbackURL

	params.Set("hub.callback", fullCallback)
	params.Set("hub.mode", "subscribe")
	params.Set("hub.topic", sub.Target.TopicURL)

	sub.pendingVerification = true
	req, err := http.NewRequest(
		"POST",
		sub.Target.HubURL,
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", UserAgentSubscriber)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

// discover makes a GET request to the URL and checks
// Link headers for a "hub", and "self".
//
// It does not discover from
// the HTML `<link>` tags,
// the XML `<link>` tags,
// or the XML `<atom:link>` tags. (as the specification says it must)
func discover(topic string) (*subscriptionTarget, error) {
	target := &subscriptionTarget{}
	req, err := http.NewRequest("GET", topic, nil)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic-url", topic).
			Msg("could create GET request for topic url")
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgentSubscriber)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic-url", topic).
			Msg("could not GET topic url")
		return nil, err
	}

	defer resp.Body.Close()

	// check for link headers
	links := linkheader.ParseMultiple(resp.Header.Values("Link"))

	for _, link := range links {
		if link.Rel == "self" {
			target.TopicURL = link.URL
		}

		if link.Rel == "hub" {
			target.HubURL = link.URL
		}
	}

	if target.TopicURL != "" && target.HubURL != "" {
		return target, nil
	}

	return nil, errors.New("could not discover topic/hub url from Link headers")
}
