package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/notnotquinn/go-websub"
)

var log = websub.Logger()

// Create a new subscriber on port :3033
//
// wait 5 seconds
//
// Subscribe to events on localhost:7070/count
func subMain() {
	s := websub.NewSubscriber(
		"http://localhost:3033/",
		websub.SubscriberWithLeaseLength(time.Hour),
	)

	go http.ListenAndServe("127.0.0.1:3033", s)
	fmt.Println("[subscriber] Listening on 127.0.0.1:3033")

	time.Sleep(time.Second * 5)
	fmt.Println("Subscribing!")

	_, err := s.Subscribe(
		"http://localhost:7070/count",
		"random secret string used for verifying the hub actually "+
			"sent you the content and not some random script kiddie",
		func(sub *websub.SubscriberSubscription, contentType string, body io.Reader) {
			fmt.Printf("Topic %s updated. %v\n", sub.Topic, time.Now().Unix())
			fmt.Printf("contentType: %v\n", contentType)
			bytes, err := io.ReadAll(body)
			if err != nil {
				panic(err)
			}
			fmt.Printf("  string(bytes): %v\n", string(bytes))
		},
	)

	if err != nil {
		panic(err)
	}
}

// create a new publisher listening on port :7070 using a hub on :8080.
//
// Generate publishes on localhost:7070/count
func pubMain() {
	p := websub.NewPublisher(
		"http://localhost:7070/",
		"http://localhost:8080/",
		websub.PublisherWithPostBodyAsContent(true),
	)

	go http.ListenAndServe("127.0.0.1:7070", p)
	fmt.Println("[publisher] Listening on 127.0.0.1:7070")

	go func() {
		ticker := time.NewTicker(time.Second * 6)

		i := 0

		for {
			fmt.Println("\n--Publish.", time.Now().Unix())
			err := p.Publish(
				"http://localhost:7070/count", "text/plain",
				[]byte("count "+fmt.Sprint(i)),
			)
			if err != nil {
				log.Err(err).Msg("could not publish")
			}
			i++
			<-ticker.C
		}
	}()
}

// create a new hub and listen on port :8080
func hubMain() {
	h := websub.NewHub(
		"http://localhost:8080/",
		websub.HubAllowPostBodyAsContent(true),
		websub.HubWithHashFunction("sha256"),
	)
	go http.ListenAndServe("127.0.0.1:8080", h)
	fmt.Println("[hub] Listening on 127.0.0.1:8080")
}

func main() {
	/*
		Each of these *_main functions could be a stand-alone process
		or entirely separated on a separate server.

		So long as they have the correct URLs in relation to eachother.
	*/

	go hubMain()
	go pubMain()
	go subMain()
	<-make(chan struct{})
}
