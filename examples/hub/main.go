package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/notnotquinn/go-websub"
)

// To try this example, use something like ngrok (free)
// to serve your local network over the internet.

// $ ngrok http 4044
// > You will get a public URL pointing to your local port 4044

// You can go to https://websub.rocks/hub to test the hub.

func main() {
	port := 4044
	h := websub.NewHub("https://public.example.com/",
		// Sets the hash function used for authenticated content distribution.
		websub.HWithHashFunction("sha256"),
		// When this is enabled, the topic h.HubUrl()+"/topics" will
		// be published whenever there is a subscription or publish
		// to a new, previously unknown topic.
		//
		// And the h.HubUrl()+"/topics" endpoint will be exposed as
		// a JSON array of all topics currently known to the hub,
		// with Link headers advertising it as a websub topic.
		websub.HExposeTopics(true),
	)

	// Add a sniffer to log all publishes that happen on this hub.
	h.AddSniffer("", func(topic, contentType string, body io.Reader) {
		content, err := io.ReadAll(body)
		if err != nil {
			panic(err) // handle
		}

		fmt.Printf("[hub] New publish: %s: %s:\n  %s\n", topic, contentType, content)
	})

	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), h)
	fmt.Printf("[hub] Listening on 127.0.0.1:%d\n", port)
}
