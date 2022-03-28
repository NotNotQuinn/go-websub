package websub

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tomnomnom/linkheader"
)

var (
	ErrNon2xxOnPubReq = errors.New("hub returned a non 2xx status code on publish request")
)

type publishedContent struct {
	contentType string
	content     []byte
}

// TODO: Publisher should redirect /topic/something/ to /topic/something
// or treat them equivialantly

type Publisher struct {
	postBodyAsContent      bool
	advertiseInvalidTopics bool
	baseUrl                string
	hubUrl                 string
	// maps id to published content
	publishedContent map[string]*publishedContent
}

func (p Publisher) BaseUrl() string {
	return p.baseUrl
}

func NewPublisher(baseUrl, hubUrl string, options ...PublisherOption) *Publisher {
	p := &Publisher{
		baseUrl:          strings.TrimSuffix(baseUrl, "/"),
		hubUrl:           hubUrl,
		publishedContent: make(map[string]*publishedContent),
	}

	for _, opt := range options {
		opt(p)
	}

	return p
}

type PublisherOption func(p *Publisher)

// PWithPostBodyAsContent sends what is normally the body as the query parameters,
// and sends the content as the body. Also adds hub.content="body" in the query parameters.
//
// Important: If the hub does not have this enabled, you will be unable to post.
func PWithPostBodyAsContent(enabled bool) PublisherOption {
	return func(p *Publisher) {
		p.postBodyAsContent = enabled
	}
}

// PAdvertiseInvalidTopics will advertise all topics with Link headers and
// return a 200 OK status as if they have already been published to with blank content.
func PAdvertiseInvalidTopics(enabled bool) PublisherOption {
	return func(p *Publisher) {
		p.advertiseInvalidTopics = enabled
	}
}
func (p *Publisher) Publish(topic string, contentType string, content []byte) error {
	if strings.HasPrefix(topic, p.baseUrl) {
		p.publishedContent[strings.TrimPrefix(topic, p.baseUrl+"/")] = &publishedContent{
			contentType: contentType,
			content:     content,
		}
	}

	return p.sendPublishRequest(topic, contentType, content)
}

func (p *Publisher) sendPublishRequest(topic, contentType string, content []byte) error {
	values := url.Values{
		"hub.mode":  []string{"publish"},
		"hub.topic": []string{topic},
		"hub.url":   []string{topic},
	}

	var hubUrl string
	var reqContentType string
	var body io.Reader

	if p.postBodyAsContent {
		parsed, err := url.Parse(p.hubUrl)
		if err != nil {
			return err
		}

		values.Add("hub.content", "body")

		parsed.RawQuery = strings.TrimPrefix(parsed.Query().Encode()+"&"+values.Encode(), "&")
		reqContentType = contentType
		hubUrl = parsed.String()
		body = bytes.NewReader(content)
	} else {
		reqContentType = "application/x-www-form-urlencoded"
		hubUrl = p.hubUrl
		body = strings.NewReader(values.Encode())
	}

	resp, err := http.Post(hubUrl, reqContentType, body)
	if err != nil {
		return err
	}

	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

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

	return nil
}

func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Method not allowed"))
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/")
	pub := p.publishedContent[id]

	if (pub == nil && !p.advertiseInvalidTopics) || id == "" {
		w.WriteHeader(404)
		return
	}

	w.Header().Add("Link", linkheader.Links{
		{
			Rel: "self",
			URL: p.baseUrl + "/" + id,
		},
		{
			Rel: "hub",
			URL: p.hubUrl,
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
