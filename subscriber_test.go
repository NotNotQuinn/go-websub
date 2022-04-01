package websub

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiscoverHTTPHeader(t *testing.T) {
	subscriber := NewSubscriber("exampleUrl")

	topic := "https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4"
	resp, err := http.Get(topic)
	if err != nil {
		t.Error(err)
		return
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
		return
	}

	self, hub, err := subscriber.discoverHTTPHeader(resp, bytes.NewReader(content))
	if err != nil {
		t.Error(err)
		return
	}

	assert.Equal(t, topic, self, "topic url mismatch")
	assert.Equal(t, topic+"/hub", hub, "hub url mismatch")
}

func TestDiscoverHTMLTag(t *testing.T) {
	subscriber := NewSubscriber("exampleUrl")

	topic := "https://websub.rocks/blog/101/ThidvEmeLekfQW4WVLKn"
	resp, err := http.Get(topic)
	if err != nil {
		t.Error(err)
		return
	}
	defer resp.Body.Close()

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Error(err)
		return
	}

	self, hub, err := subscriber.discoverHTMLTag(resp, bytes.NewReader(content))
	if err != nil {
		t.Error(err)
		return
	}

	assert.Equal(t, topic, self, "topic url mismatch")
	assert.Equal(t, topic+"/hub", hub, "hub url mismatch")
}
