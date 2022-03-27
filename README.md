# go-websub

go-websub is a websub subcriber and publisher written in go.

This was written before I knew about https://github.com/tystuyfzand/websub-client.
It is the sort of implementation I was looking for.

## Subscriber:

A conforming subscriber:

Must:
 *	MUST support each discovery mechanism in the specified order
    to discover the topic and hub URLs as described in [Discovery]
	(https://www.w3.org/TR/websub/#discovery) (only checks the headers, but check)
 *	MUST send a subscription request as described in [Subscriber
    Sends Subscription Request] (check)
	(https://www.w3.org/TR/websub/#subscriber-sends-subscription-request).
 *	MUST acknowledge a content distribution request with an
    HTTP 2xx status code. (check)

May:
 *	MAY request a specific lease duration (nop lol)
 *	MAY request that a subscription is deactivated using the "unsubscribe"
    mechanism. (nop lol)
 *	MAY include a secret in the subscription request, and if it does, then
    MUST use the secret to verify the signature in the [content distribution
	request](https://www.w3.org/TR/websub/#authenticated-content-distribution). (nop lol)


## Publisher:

A conforming publisher MUST advertise topic and hub URLs for a given resource URL
as described in [Discovery](https://www.w3.org/TR/websub/#discovery).

To publish, POST request with the keys hub.mode="publish" and hub.url=(the URL of the resource that was updated).

## Hub:

A conforming hub:

 *	MUST accept a subscription request with the parameters hub.callback, hub.mode and hub.topic.
 *	MUST accept a subscription request with a hub.secret parameter.
 *	MAY respect the requested lease duration in subscription requests.
 *	MUST allow subscribers to re-request already active subscriptions.
 *	MUST support unsubscription requests.
 *	MUST send content distribution requests with a matching content type of the topic URL. (See Content Negotiation)
 *	MAY reduce the payload of the content distribution to a diff of the contents for supported formats as described in Content Distribution.
 *	MUST send a X-Hub-Signature header if the subscription was made with a hub.secret as described in Authenticated Content Distribution.
