package websub

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tomnomnom/linkheader"
)

var (
	// hub returned a non 2xx status code on publish request
	ErrNon2xxOnPubReq = errors.New("hub returned a non 2xx status code on publish request")
)

type publishedContent struct {
	contentType string
	content     []byte
}

type Publisher struct {
	postBodyAsContent      bool
	advertiseInvalidTopics bool
	baseURL                string
	hubURL                 string
	// maps id to published content
	publishedContent map[string]*publishedContent
	// guard for publishedContent
	mu *sync.RWMutex
}

// BaseURL returns the base URL of this publisher (with any trailing slash trimmed)
func (p *Publisher) BaseURL() string {
	return p.baseURL
}

func NewPublisher(baseURL, hubURL string, options ...PublisherOption) *Publisher {
	p := &Publisher{
		baseURL:          strings.TrimSuffix(baseURL, "/"),
		hubURL:           hubURL,
		publishedContent: make(map[string]*publishedContent),
		mu:               &sync.RWMutex{},
	}

	for _, opt := range options {
		opt(p)
	}

	return p
}

type PublisherOption func(p *Publisher)

// PublisherWithPostBodyAsContent sends what is normally the body as the query parameters,
// and sends the content as the body. Also adds hub.content="body" in the query parameters.
//
// Important: If the hub does not have this enabled, you will be unable to publish.
func PublisherWithPostBodyAsContent(enabled bool) PublisherOption {
	return func(p *Publisher) {
		p.postBodyAsContent = enabled
	}
}

// PublisherAdvertiseInvalidTopics will advertise all topics with Link headers and
// return a 200 OK status as if they have already been published to with blank content.
func PublisherAdvertiseInvalidTopics(enabled bool) PublisherOption {
	return func(p *Publisher) {
		p.advertiseInvalidTopics = enabled
	}
}

// Publish will send a publish request to the hub.
//
// If the topic URL starts with this publisher's base URL, the publisher
// will return the content on HTTP GET requests to that url.
func (p *Publisher) Publish(topic string, contentType string, content []byte) error {
	if strings.HasPrefix(topic, p.baseURL+"/") {
		// "https://example.com/baseURL/topic/1////" gets stored as "topic/1"
		// removing a trailing slash
		p.mu.Lock()
		p.publishedContent[strings.Trim(strings.TrimPrefix(topic, p.baseURL+"/"), "/")] = &publishedContent{
			contentType: contentType,
			content:     content,
		}
		p.mu.Unlock()
	}

	return p.sendPublishRequest(topic, contentType, content)
}

// sendPublishRequest sends an HTTP POST request to the hub.
//
// if p.postBodyAsContent is true, it sends the content as the body,
// otherwise the content and contentType are ignored.
func (p *Publisher) sendPublishRequest(topic, contentType string, content []byte) error {
	values := url.Values{
		"hub.mode":  []string{"publish"},
		"hub.topic": []string{topic},
		"hub.url":   []string{topic},
	}

	var hubURL string
	var reqContentType string
	var body io.Reader

	if p.postBodyAsContent {
		parsed, err := url.Parse(p.hubURL)
		if err != nil {
			return err
		}

		values.Add("hub.content", "body")

		parsed.RawQuery = strings.TrimPrefix(parsed.Query().Encode()+"&"+values.Encode(), "&")
		reqContentType = contentType
		hubURL = parsed.String()
		body = bytes.NewReader(content)
	} else {
		reqContentType = "application/x-www-form-urlencoded"
		hubURL = p.hubURL
		body = strings.NewReader(values.Encode())
	}

	resp, err := http.Post(hubURL, reqContentType, body)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Err(err).
				Int("status-code", resp.StatusCode).
				Msg("could not publish")
			return err
		}

		log.Err(ErrNon2xxOnPubReq).
			Int("status-code", resp.StatusCode).
			Str("body", string(bytes)).
			Msg("could not publish")
		// Non 2xx
		return ErrNon2xxOnPubReq
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}

// ServeHTTP serves the content that has been published to this publisher,
// and advertises topic and hub urls in Link headers.
//
// Only topics published with a URL that starts with the base URL are advertised.
func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}

	// request to "//////topic/1/////" gets treated as equal to "/topic/1/"
	// stored as "topic/1"
	id := strings.Trim(r.URL.Path, "/")
	p.mu.RLock()
	pub := p.publishedContent[id]
	p.mu.RUnlock()

	if (pub == nil && !p.advertiseInvalidTopics) || id == "" {
		w.WriteHeader(404)
		return
	}

	w.Header().Add("Link", linkheader.Links{
		{
			Rel: "self",
			URL: p.baseURL + "/" + id,
		},
		{
			Rel: "hub",
			URL: p.hubURL,
		},
	}.String())

	if pub == nil && p.advertiseInvalidTopics {
		w.WriteHeader(200)
		return
	}

	w.Header().Add("Content-Type", pub.contentType)
	w.WriteHeader(200)
	w.Write(pub.content)
}
