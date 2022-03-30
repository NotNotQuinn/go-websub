// Adapted from sniffer and added topic announcements
package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/notnotquinn/go-websub"
)

var log = websub.Logger()

func main() {
	// initialize subscriber, hub, and publisher.
	baseUrl := "http://localhost:3033"
	mux := http.NewServeMux()

	s := websub.NewSubscriber(
		baseUrl+"/sub/",
		websub.SubscriberWithLeaseLength(time.Hour),
	)

	p := websub.NewPublisher(
		baseUrl+"/topic/",
		baseUrl+"/hub/",
		websub.PublisherWithPostBodyAsContent(true),
		websub.PublisherAdvertiseInvalidTopics(true),
	)

	h := websub.NewHub(
		baseUrl+"/hub/",
		websub.HubAllowPostBodyAsContent(true),
		websub.HubWithHashFunction("sha256"),
		websub.HubExposeTopics(true),
	)

	h.AddSniffer("", func(topic, contentType string, body io.Reader) {
		bytes, err := io.ReadAll(body)
		if err != nil {
			log.Err(err).Msg("error reading body in sniffer")
		}
		fmt.Printf("[global sniffer] new publish:\n      topic: %s\n      content-type: %s\n      body: %s\n", topic, contentType, string(bytes))
	})

	// register handlers
	mux.Handle("/sub/", http.StripPrefix("/sub", s))
	mux.Handle("/topic/", http.StripPrefix("/topic", p))
	mux.Handle("/hub/", http.StripPrefix("/hub", h))

	// listen for requests
	go http.ListenAndServe("127.0.0.1:3033", mux)
	fmt.Println("Listening on 127.0.0.1:3033")

	// publish every 6 seconds
	go func() {
		ticker := time.NewTicker(time.Second * 6)

		i := 0

		for {
			<-ticker.C

			fmt.Println("\n--Publish.", time.Now().Unix())
			err := p.Publish(
				baseUrl+"/topic/count",
				"text/plain",
				[]byte("count "+fmt.Sprint(i)),
			)
			if err != nil {
				log.Err(err).Msg("could not publish")
			}
			i++
		}
	}()

	time.Sleep(time.Second * 5)

	fmt.Println("Subscribing!")

	printSubscription := func(sub *websub.SubscriberSubscription, contentType string, body io.Reader) {
		fmt.Printf("[subscription] new publish:\n")
		fmt.Printf("      topic: %v\n", sub.Topic)
		fmt.Printf("      content-type: %v\n", contentType)
		bytes, err := io.ReadAll(body)
		if err != nil {
			panic(err)
		}
		fmt.Printf("      body: %v\n", string(bytes))
	}

	// Important: You must publish at least once before subscribing
	// unless you use websub.PAdvertiseInvalidTopics(true) on the publisher
	// otherwise you will be unable to subscribe. (because the topic doesnt exist)

	// subscribe to the hub finding new topics
	_, err := s.Subscribe(
		baseUrl+"/hub/topics",
		"random secret string",
		printSubscription,
	)

	if err != nil {
		panic(err)
	}

	// subscribe to a topic
	_, err = s.Subscribe(
		baseUrl+"/topic/count",
		"random secret string",
		printSubscription,
	)

	if err != nil {
		panic(err)
	}
	<-make(chan struct{})
}
