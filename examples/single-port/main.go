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
		websub.SWithLeaseLength(time.Hour),
	)

	p := websub.NewPublisher(
		baseUrl+"/topic/",
		baseUrl+"/hub/",
		websub.PWithPostBodyAsContent(true),
		websub.PAdvertiseInvalidTopics(true),
	)

	h := websub.NewHub(
		baseUrl+"/hub/",
		websub.HAllowPostBodyAsContent(true),
		websub.HWithHashFunction("sha256"),
	)

	// register handlers
	mux.Handle("/sub/", http.StripPrefix("/sub/", s))
	mux.Handle("/topic/", http.StripPrefix("/topic/", p))
	mux.Handle("/hub/", http.StripPrefix("/hub/", h))

	// listen for requests
	go http.ListenAndServe("127.0.0.1:3033", mux)
	fmt.Println("Listening on 127.0.0.1:3033")

	// publish every 6 seconds
	go func() {
		ticker := time.NewTicker(time.Second * 6)

		i := 0

		for {
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
			<-ticker.C
		}
	}()

	time.Sleep(time.Second * 5)

	fmt.Println("Subscribing!")

	// Important: You must publish at least once before subscribing
	// unless you use websub.PAdvertiseInvalidTopics(true) on the publisher
	// otherwise you will be unable to subscribe. (because the topic doesnt exist)

	// subscribe to a topic
	_, err := s.Subscribe(
		baseUrl+"/topic/count",
		"random secret string",
		func(sub *websub.SSubscription, contentType string, body io.Reader) {
			fmt.Printf("Topic %s updated. %v\n", sub.Topic, time.Now().Unix())
			fmt.Printf("contentType: %v\n", contentType)
			bytes, err := io.ReadAll(body)
			if err != nil {
				panic(err)
			}
			fmt.Printf("string(bytes): %v\n", string(bytes))
		},
	)

	if err != nil {
		panic(err)
	}

	<-make(chan struct{})
}
