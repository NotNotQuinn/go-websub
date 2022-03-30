/*
	Interactive example of publishing content using go-websub.
*/
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/notnotquinn/go-websub"
)

func main() {
	hubPort := 4044
	publisherPort := 3033

	fmt.Printf("Ensure port %d and %d are free on your system.\n", hubPort, publisherPort)

	// Start a hub
	startHub(hubPort)

	p := websub.NewPublisher(
		// Base URL for publisher
		fmt.Sprintf("http://localhost:%d/", publisherPort),
		// Hub URL
		fmt.Sprintf("http://localhost:%d/", hubPort),
	)

	// The publisher must be exposed & listening to expose the topic URLs
	// so the hub can make a GET request to retrieve the content to distribute.
	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", publisherPort), p)
	fmt.Printf("[publisher] Listening on 127.0.0.1:%d\n", publisherPort)

	fmt.Println("[publisher] Publishing!")
	err := p.Publish(
		// Topic URL ("http://localhost:3033/helloWebSub")
		p.BaseUrl()+"/helloWebSub",
		// Content Type
		"text/plain",
		// Content
		[]byte("Hello, WebSub."),
	)

	if err != nil {
		panic(err) // handle
	}

	// After publishing, the content is available at
	// http://localhost:3033/helloWebSub
	// with the proper Link headers advertising the hub and self URLs.

	// If you need the topic to be advertised with hub and self URLs before it is published to,
	// use the websub.PAdvertiseInvalidTopics(true) option when creating the publisher.

	// When a second publish is made the content is replaced,
	// and the hub will GET the content before sending it to
	// subscribers via POST request.

	// When a publish is made with a publisher that uses websub.PWithPostBodyAsContent(true)
	//  3 things change:
	//    - The hub must support posting content as the POST body.
	//      Only this implementation does, at the time of writing,
	//      and it is disabled by default for security. If the hub doesn't
	//      support it, or it is disabled, you will likely get a 4xx or 5xx response.
	//    - The publisher sends the content directly to the hub, in the POST request.
	//    - The hub does NOT make a GET request to retrieve the content.
	//
	// The content is still served over HTTP, even though it will never be retrieved.
	//
	// This was implemented to allow an internal network of publishers to post on topic
	// URLs that they don't "own" (enabling many-to-many communication, rather than one-to-many)
	// and generally isnt useful otherwise.
	//
	// The publisher CAN send publish requests for URLs not under its
	// base URL, for the reason mentioned above, but doesn't serve (or save)
	// that content via HTTP anywhere.

	fmt.Printf("Does http://localhost:%d/helloWebSub say \"Hello, WebSub.\"?\n", publisherPort)
	fmt.Println("Waiting 10 seconds before continuing.")
	time.Sleep(10 * time.Second)

	fmt.Println("[publisher] Publishing again!")
	err = p.Publish(
		// Topic URL ("http://localhost:3033/helloWebSub")
		p.BaseUrl()+"/helloWebSub",
		// Content Type
		"text/plain",
		// Content
		[]byte("Goodbye, WebSub!"),
	)

	if err != nil {
		panic(err) // handle
	}

	// The content at
	// http://localhost:3033/helloWebSub
	// should now be "Goodbye, WebSub!" rather than "Hello, WebSub."
	// and any subscribers will have received a POST request from the hub
	// containing "Goodbye, WebSub!"

	fmt.Println("Example finished.")
	fmt.Printf("Does http://localhost:%d/helloWebSub say \"Goodbye, WebSub!\"?\n", publisherPort)

	<-make(chan struct{})
}

// Serve a new Hub on the specified port
//
// utility used for example
func startHub(port int) {
	h := websub.NewHub(fmt.Sprintf("http://localhost:%d/", port))
	// Empty topic = sniff all topics
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
