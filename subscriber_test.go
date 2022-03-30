package websub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiscover(t *testing.T) {
	subscriber := NewSubscriber("exampleUrl")

	self, hub, err := subscriber.discover("https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4")
	if err != nil {
		panic(err)
	}

	assert.Equal(t, "https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4", self, "")
	assert.Equal(t, "https://websub.rocks/blog/100/wtaLpbuXsr0CiFyU0lI4/hub", hub, "")
}
