# go-websub

go-websub is a websub subscriber, publisher, and hub library written
in go. It passes all of the [websub.rocks](https://websub.rocks/)
tests for publishers and hubs, and *almost* all of them for subscribers.

Inspired by https://github.com/tystuyfzand/websub-client
and https://github.com/tystuyfzand/websub-server.


## Examples

See the [./examples/](./examples/) directory for examples of using
a [subscriber](./examples/subscriber/main.go),
[publisher](./examples/publisher/main.go), and [hub](./examples/hub/main.go).


## Importing

```
go get github.com/notnotquinn/go-websub
```
## Quick links

 - [Features](#features)
	- [Subscriber features](#subscriber-features) - [methods](#subscriber-methods)
	- [Publisher features](#publisher-features) - [methods](#publisher-methods)
	- [Hub features](#hub-features) - [methods](#hub-methods)
 - [Spec conformance classes](#spec-conformance-classes)
	- [Subscriber conformance](#subscriber-conformance)
	- [Publisher conformance](#hub-conformance)
	- [Hub conformance](#hub-conformance)

## Features

 - [ ] BIG: Does not (currently) persist state between restarts.
 - [x] Can run subscriber, publisher, and hub from one server, on one port, if needed.
       (see [examples/single-port](./examples/single-port/main.go))

### Subscriber features

 - [ ] BIG: Does not (currently) persist state between restarts.
 - [ ] Not completely spec compliant. (see [Subscriber conformance](#subscriber-conformance))
 - [x] Functions independently from publisher and hub.
 - [x] Subscribe & unsubscribe to topics.
 - [x] Simple function callback system, with access to subscription meta-data.
 - [x] Request specific lease duration.
 - [x] Provide `hub.secret` and verify hub signature.
 - [ ] Automatically refresh expiring subscriptions.

#### Subscriber methods

Interact with the subscriber via a Go API:
 - [x] Subscribe to topic URLs with callback.
    - Example:
	```go
	subscription, err := s.Subscribe(
		// Topic URL that exposes Link headers for "hub" and "self".
		"https://example.com/topic1",
		// for authenticated content distribution (maximum of 200 characters)
		"random bytes",
		// Callback function is called when the subscriber receives a valid
		// request from the hub, not on invalid ones
		// (for example ones with a missing or invalid hub signature)
		func(sub *websub.SubscriberSubscription, contentType string, body io.Reader) {
			fmt.Println("Received content!")
			// do something...
		},
	)

	if err != nil {
		// handle
	}
	```
 - [x] Unsubscribe from topics.
    - Example:
	```go
	err = s.Unsubscribe(subscription)

	if err != nil {
		// handle
	}
	```


### Publisher features

 - [ ] BIG: Does not (currently) persist state between restarts.
 - [x] Completely spec compliant.
 - [x] Functions independently from subscriber and hub.
 - [x] Advertise topic and hub URLs for previously published topics.
 - [x] Send publish requests for topic URLs that arent under the publishers base URL.
 - [x] Send both `hub.topic` and `hub.url` on publish requests.
 - [x] Treat `https://example.com/baseURL/topic/` equal to `https://example.com/baseURL/topic`
       for incoming requests.
 - [x] Optionally advertise topic and hub URLs for unpublished topics.
 - [x] Optionally post content in publish request. (see [publisher publishing
       methods](#publisher-publishing-methods) #2)

#### Publisher methods

Interact with the publisher via a Go API:
 - [x] Publish content with content-type.
    - Example:
	```go
	err = p.Publish(
		// Topic URL
		p.BaseURL()+"/topic1",
		// Content Type
		"text/plain",
		// Content
		[]byte("Hello, WebSub!"),
	)

	if err != nil {
		// handle
	}
	```

### Hub features

 - [ ] BIG: Does not (currently) persist state between restarts.
 - [x] Completely spec compliant.
 - [x] Functions independently from subscriber and publiser.
 - [x] Configurable retry limits for distribution requests.
 - [x] Configurable lease length limits and default lease length.
 - [x] Configurable User-Agent for HTTP requests on behaf of the hub.
 - [x] Configure one of "sha1", "sha256", "sha384", and "sha512" for signing publish requests.
 - [x] Optionally expose all known topic URLs as a JSON array to `/topics` and
       provide websub updates for new topics.
 - [x] Optionally accept publish requests with the body as the content.
       (see [Hub accepted publishing methods](#hub-accepted-publishing-methods) #2)

#### Hub methods

Interact with the hub via a Go API:
 - [x] Custom subscription validation.
    - Example:
	```go
	// Deny any subscription where the callback URL is not under "example.com"
	h.AddValidator(func(sub *websub.HubSubscription) (ok bool, reason string) {
		parsed, err := url.Parse(sub.Callback)
		if err != nil {
			return false, "invalid callback url"
		}

		if parsed.Host != "example.com" {
			return false, "callback host not allowed"
		}

		return true, ""
	})
	```
 - [x] Sniff on published topics. (to all or one topic, as if you were subscribed)
    - Example:
    ```go
	// List the topic url as an empty string to listen to all publishes.
	h.AddSniffer("https://example.com/topic1",
	    func(topic, contentType string, content []byte) {
			fmt.Printf("New publish on \"https://example.com/topic1\" !")
		},
	)
	```
 - [x] Publish content via method call.
    - Example:
    ```go
	// no return value
	h.Publish("https://example.com/topic1", "Content-Type", []byte("Content"))
	```
 - [x] Get all topic URLs programmatically.
    - Example:
    ```go
	fmt.Printf("%#v", h.GetTopics())
	// []string{"https://example.com/topic1", https://example.com/topic2", ...}
    ```

## Spec conformance classes

As per the [websub spec](https://www.w3.org/TR/websub/#conformance-classes).

### Subscriber conformance

The included subscriber technically does not follow the websub spec,
because it does not support discovering links from HTML tags, Atom
feeds, or RSS feeds.

The subscriber still discovers topics form their `Link` headers,
so this does not impact the subscriber's interaction with the hub
or publisher implemented here, but it may end up being a problem
if you are planning on subscribing to other publisher implementations
that don't provide `Link` headers for their topics.

A conforming subscriber:
 - [2/5] MUST support each discovery mechanism in the specified order
    to discover the topic and hub URLs as described in [Discovery
	](https://www.w3.org/TR/websub/#discovery)
	- [x] HTTP header discovery
	- [ ] HTML tag discovery
	- [ ] Atom feed discovery
	- [ ] RSS feed discovery
	- [x] Discovery priority
 - [x] MUST send a subscription request as described in
      [Subscriber Sends Subscription Request
	  ](https://www.w3.org/TR/websub/#subscriber-sends-subscription-request).
 - [x] MUST acknowledge a content distribution request with an
    HTTP 2xx status code.
 - [x] MAY request a specific lease duration
 - [x] MAY request that a subscription is deactivated using the "unsubscribe"
    mechanism.
 - [x] MAY include a secret in the subscription request, and if it does, then
    MUST use the secret to verify the signature in the [content distribution
	request](https://www.w3.org/TR/websub/#authenticated-content-distribution).


### Publisher conformance

A conforming publisher:
 - [x] MUST advertise topic and hub URLs for a given resource
       URL as described in [Discovery](https://www.w3.org/TR/websub/#discovery).


#### Publisher publishing methods

There are 2 options to publish, which are both supported by the hub:

 1. (default) Send a POST request (as Content-Type: `application/x-www-form-urlencoded`)
    with the keys `hub.mode` set to `publish`, and `hub.url` & `hub.topic` both
    set to the updated URL in the body as a form.

 2. (opt-in) Send a POST request with the same keys as above
    in the query string parameters, and the key `hub.content` equal to `body`.
    The hub will not make any request to the topic URL, and instead will
    distribute the body of the POST request, and associated Content-Type to
    subscribers. This method is disabled by default for the hub.

The updated URL is duplicated because of possible hub implementation variations.


### Hub conformance

A conforming hub:

 - [x] MUST accept a subscription request with the parameters
       hub.callback, hub.mode and hub.topic.
 - [x] MUST accept a subscription request with a hub.secret parameter.
 - [x] MAY respect the requested lease duration in subscription requests.
   - Respects requested lease duration if it is within a configurable allowed
     range, otherwise it is pinned to the maximum or minimum limit.
 - [x] MUST allow subscribers to re-request already active subscriptions.
 - [x] MUST support unsubscription requests.
 - [x] MUST send content distribution requests with a matching content type
       of the topic URL. (See [Content Negotiation
	   ](https://www.w3.org/TR/websub/#content-negotiation))
 - [x] MUST send a X-Hub-Signature header if the subscription was made with
       a hub.secret as described in [Authenticated Content Distribution
	   ](https://www.w3.org/TR/websub/#authenticated-content-distribution).
 - [ ] MAY reduce the payload of the content distribution to a diff of the
       contents for supported formats as described in [Content Distribution
	   ](https://www.w3.org/TR/websub/#content-distribution).


#### Hub accepted publishing methods

There are two ways for publishers to publish to the hub, which match the ones
availible for the provided publisher:

 1. Send a POST request (as Content-Type: `application/x-www-form-urlencoded`)
    with the keys `hub.mode` equal to `publish`, one or both of `hub.topic` and
    `hub.url` equal to the topic URL that was updated in the body as a form.
    If both are provided `hub.topic` is used. The hub makes a GET request
    to the topic URL and distributes the content to subscribers, with the correct
    Content-Type.

 2. (disabled by default) Send a POST request with the same keys as above
    in the query string parameters, and the key `hub.content` equal to `body`.
    The hub will not make any request to the topic URL, and instead will
    distribute the body of the POST request, and associated Content-Type to
    subscribers.
