package websub

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tomnomnom/linkheader"
)

var (
	// PublisherUrlPrefix is prepended to topic names to create unique topic urls
	//
	// Default: "websub/t"
	PublisherUrlPrefix string = "websub/t"

	// Keeps track of registered topic Paths in the serveMux
	registeredPaths = make(map[string]bool)

	// Maps topic URLs to topic objects
	topics = make(map[string]*topic)
)

type topic struct {
	// The hubs that we are broadcasting updates for this topic to
	HubURLs []string `json:"hubURLs"`

	// The topic url for this topic
	TopicURL string `json:"topicURL"`

	// The path registered with the serveMux
	ServedPath string `json:"servedPath"`
}

// TODO: Allow publishers to have their own URL prefix
// (fixes issue noted below)

// A publisher publishes all topics to multiple hubs at once
//
// Note: If the topic URLs have overlap, all hubs will always get
// all updates for those urls.
// For example if you have 2 publishers publish same topic URL,
// both groups of hubs will get *all* updates from both publishers.
type Publisher struct {
	// List of hub URLs that this publisher will publish to.
	HubURLs []string
}

// Creates a new publisher for the listed hub URLs
func NewPublisher(hubUrls ...string) Publisher {
	return Publisher{HubURLs: hubUrls}
}

// Publish publishes a topic to all hubs for this publisher asynchronously.
func (p Publisher) Publish(topicName, contentType string, content []byte) <-chan error {
	return p.PublishURL(TopicURL(topicName), contentType, content)
}

// Publish publishes an arbitrary topic URL to all hubs for this publisher asynchronously.
func (p Publisher) PublishURL(topicURL, contentType string, content []byte) <-chan error {
	errs := make(chan error, len(p.HubURLs))
	for _, hubUrl := range p.HubURLs {
		go func(hubUrl string) {
			err := PublishURL(hubUrl, topicURL, contentType, content)
			if err != nil {
				log.Error().
					Err(err).
					Str("hubUrl", hubUrl).
					Str("topicURL", topicURL).
					Msg("could not publish topic to hub")
				errs <- fmt.Errorf("could not publish topic to hub: %w", err)
			}
		}(hubUrl)
	}
	return errs
}

// Publish directly performs a publish to a specific hub
// and keeps it up to date with new content
func Publish(hubURL, topicName, contentType string, content []byte) error {
	return PublishURL(hubURL, TopicURL(topicName), contentType, content)
}

// Variant of Publish, which publishes an arbitrary topic URL to a
// specific hub, and keeps the hub updated on any new content for this topic URL
func PublishURL(hubURL, topicURL, contentType string, content []byte) error {
	parsedTopic, err := url.Parse(topicURL)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic-url", topicURL).
			Msg("Could not parse topic URL")
		return err
	}

	parsedBase, err := url.Parse(BaseURL)
	if err != nil {
		log.Error().
			Err(err).
			Str("base-url", BaseURL).
			Msg("Could not parse BaseURL")
		return err
	}

	sameOrigin := parsedBase.Host == parsedTopic.Host

	servedPath := parsedTopic.EscapedPath()
	if !sameOrigin {
		servedPath = ""
	}

	topic := getTopic([]string{hubURL}, topicURL, servedPath)
	if sameOrigin && !registeredPaths[topic.ServedPath] && topic.ServedPath != "" {
		// Register an HTTP callback for this topic URL
		// and advertise the hubs and self in link Headers

		registeredPaths[topic.ServedPath] = true
		serveMux.Handle(
			topic.ServedPath,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("User-Agent", UserAgentPublisher)
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					links := linkheader.Links{{
						URL: topicURL,
						Rel: "self",
					}}

					for _, topicHubUrl := range topic.HubURLs {
						links = append(links, linkheader.Link{
							URL: topicHubUrl,
							Rel: "hub",
						})
					}

					w.Header().Add("Link", links.String())
					// TODO: Should we provide content-type even though there is no content?
					// w.Header().Add("Content-Type", topic.contentType)
					w.WriteHeader(200)
				} else {
					w.WriteHeader(405)
					w.Write([]byte("Method Not Allowed"))
				}
			}),
		)
	}

	err = postPublishRequest(hubURL, topicURL, contentType, content)
	if err != nil {
		log.Error().
			Err(err).
			Str("hubUrl", hubURL).
			Str("topicUrl", topicURL).
			Str("contentType", contentType).
			Str("contentLength", fmt.Sprint(len(content))).
			Msg("could not post publish request")
		return err
	}

	return nil
}

func postPublishRequest(hubURL, topicURL, contentType string, content []byte) error {
	url, err := url.Parse(hubURL)
	if err != nil {
		return err
	}

	q := url.Query()

	q.Set("hub.mode", "publish")
	// Some hubs use "hub.topic" and some use "hub.url"
	q.Set("hub.topic", topicURL)
	q.Set("hub.url", topicURL)

	url.RawQuery = q.Encode()
	requestedUrl := url.String()

	req, err := http.NewRequest("POST", requestedUrl, bytes.NewReader(content))
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", UserAgentPublisher)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Error().
			Err(err).
			Str("request-sent", requestedUrl).
			Str("content-type", contentType).
			Str("content-length", fmt.Sprint(len(content))).
			Str("status-code", fmt.Sprint(resp.StatusCode)).
			Msg("hub responded with non-2xx status code")
		return errors.New("hub responded with non-2xx status code")
	}

	return nil
}

// getTopic updates or creates a topic with the provided data, and returns the new topic
func getTopic(hubURLs []string, topicURL, servedPath string) *topic {
	if topics[topicURL] == nil {
		topics[topicURL] = &topic{
			HubURLs:    hubURLs,
			TopicURL:   topicURL,
			ServedPath: servedPath,
		}
	} else {
		updateTopic(topics[topicURL], hubURLs)
	}
	return topics[topicURL]
}

func updateTopic(topic *topic, additionalHubURLs []string) {
	if topic == nil {
		return
	}

	// add any missing hub URLs
	for _, url := range additionalHubURLs {
		if !stringSliceContains(topic.HubURLs, url) {
			topic.HubURLs = append(topic.HubURLs, url)
		}
	}
}

// check if string slice contains an item
func stringSliceContains(s []string, query string) bool {
	for _, item := range s {
		if item == query {
			return true
		}
	}
	return false
}

// TopicURL returns the URL based off of BaseURL and PublisherUrlPrefix for this name
//
// The topicName can contain any valid URL characters, "key/a" and "key-b"
// are examples of valid topics.
func TopicURL(topicName string) string {
	if PublisherUrlPrefix != "" {
		return strings.TrimRight(BaseURL, "/") + "/" + PublisherUrlPrefix + "/" + topicName
	}

	return strings.TrimRight(BaseURL, "/") + "/" + topicName
}
