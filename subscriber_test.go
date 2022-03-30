package websub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiscoverHTTPHeader(t *testing.T) {
	subscriber := NewSubscriber("exampleUrl")

	self, hub, err := subscriber.discoverHTTPHeader("https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4")
	if err != nil {
		panic(err)
	}

	assert.Equal(t, "https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4", self, "")
	assert.Equal(t, "https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4/hub", hub, "")
}

func TestDiscoverHTMLTag(t *testing.T) {
	subscriber := NewSubscriber("exampleUrl")

	self, hub, err := subscriber.discoverHTMLTag("https://websub.rocks/blog/101/ThidvEmeLekfQW4WVLKn")
	if err != nil {
		panic(err)
	}

	assert.Equal(t, "https://websub.rocks/blog/101/ThidvEmeLekfQW4WVLKn", self, "")
	assert.Equal(t, "https://websub.rocks/blog/101/ThidvEmeLekfQW4WVLKn/hub", hub, "")
}
