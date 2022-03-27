# go-websub

go-websub is a websub subcriber, publisher, and hub library written in go. It passes *almost* all of the [websub.rocks](https://websub.rocks/) tests.

Inspired by https://github.com/tystuyfzand/websub-client and https://github.com/tystuyfzand/websub-server.

Failing tests:
 - Subscriber:
    - 101 HTML tag discovery
    - 102 Atom feed discovery
    - 103 RSS feed discovery

Tests not currently working (500 internal server error when clicking publish):
 - Subscriber:
	- 301 Rejects a distribution request with an invalid signature (independently verified)
	- 302 Rejects a distribution request with no signature when the subscription was made with a secret (independently verified)


# Spec conformance classes

As per the [websub spec](https://www.w3.org/TR/websub/#conformance-classes).

## Subscriber:

The included subscriber technically does not follow the websub spec, because it does not support discovering links from HTML tags, Atom feeds, or RSS feeds.

The subscriber still discovers topics form their `Link` headers, so this does not impact the subscriber's interaction with the hub or publisher implemented here, but it may end up being a problem if you are planning on subscribing to other publisher implementations that don't provide `Link` headers for their topics.

A conforming subscriber:
 - [2/5] MUST support each discovery mechanism in the specified order
    to discover the topic and hub URLs as described in [Discovery]
	(https://www.w3.org/TR/websub/#discovery)
	- [x]   HTTP header discovery
	- [ ]   HTML tag discovery
	- [ ]   Atom feed discovery
	- [ ]   RSS feed discovery
	- [x]   Discovery priority
- [x] MUST send a subscription request as described in [Subscriber
    Sends Subscription Request]
	(https://www.w3.org/TR/websub/#subscriber-sends-subscription-request).
 - [x] MUST acknowledge a content distribution request with an
    HTTP 2xx status code.
- [x] MAY request a specific lease duration
- [x] MAY request that a subscription is deactivated using the "unsubscribe"
    mechanism.
- [x] MAY include a secret in the subscription request, and if it does, then
    MUST use the secret to verify the signature in the [content distribution
	request](https://www.w3.org/TR/websub/#authenticated-content-distribution).


## Publisher:

- [x] A conforming publisher MUST advertise topic and hub URLs for a given resource URL
as described in [Discovery](https://www.w3.org/TR/websub/#discovery).

There are 2 options to publish:
 - (default) Send a POST request with the keys `hub.mode=publish`, `hub.url=https://updated-content.example.com/` and `hub.topic=https://updated-content.example.com/` in the body as a form.
 - (opt-in) Send a POST request with the keys `hub.mode=publish`, `hub.content=body`, `hub.url=https://updated-content.example.com/` and `hub.topic=https://updated-content.example.com/` in the query arguments. The body will be the content being posted, with the apropriate content-type.

The updated URL is duplicated because of possible hub implementation variations.


## Hub:

A conforming hub:

 - [x] MUST accept a subscription request with the parameters hub.callback, hub.mode and hub.topic.
 - [x] MUST accept a subscription request with a hub.secret parameter.
 - [x] MAY respect the requested lease duration in subscription requests.
   - Respects requested lease duration if it is within an allowed range, otherwise it is pinned to the maximum or minimum limit.
 - [x] MUST allow subscribers to re-request already active subscriptions.
 - [x] MUST support unsubscription requests.
 - [x] MUST send content distribution requests with a matching content type of the topic URL. (See Content Negotiation)
 - [x] MUST send a X-Hub-Signature header if the subscription was made with a hub.secret as described in Authenticated Content Distribution.
 - [ ] MAY reduce the payload of the content distribution to a diff of the contents for supported formats as described in Content Distribution.
