package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/notnotquinn/go-websub"
)

var log = websub.Logger()

func sub_main() {
	s := websub.NewSubscriber(
		"http://desktop.qdt.home.arpa:3033/",
		websub.SWithLeaseLength(time.Hour),
	)

	// http.Handle("/sub", s.ServeMux)

	go http.ListenAndServe(":3033", s)
	fmt.Println("[subscriber] Listening on :3033")

	time.Sleep(time.Second * 5)
	fmt.Println("Subscribing!")

	_, err := s.Subscribe("http://desktop.qdt.home.arpa:7070/urmomlole",
		func(contentType string, body io.Reader) {
			fmt.Println("Received event.", time.Now().Unix())
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
}

func pub_main() {
	p := websub.NewPublisher(
		"http://desktop.qdt.home.arpa:7070/",
		"http://desktop.qdt.home.arpa:8080/",
		websub.PWithPostBodyAsContent(true),
	)

	go http.ListenAndServe(":7070", p)
	fmt.Println("[publisher] Listening on :7070")

	go func() {
		ticker := time.NewTicker(time.Second * 6)

		i := 0

		for {
			fmt.Println("\n--Publish.", time.Now().Unix())
			err := p.Publish(
				"http://desktop.qdt.home.arpa:7070/urmomlole", "text/html",
				[]byte("<h1><strong>urmom lolexd "+fmt.Sprint(i)+"</strong></strong>"),
			)
			if err != nil {
				log.Err(err).Msg("could not publish")
			}
			i++
			<-ticker.C
		}
	}()
}

func hub_main() {
	h := websub.NewHub(
		"http://desktop.qdt.home.arpa:8080/",
		websub.HAllowPostBodyAsContent(true),
		websub.HWithHashFunction("sha256"),
	)
	go http.ListenAndServe(":8080", h)
	fmt.Println("[hub] Listening on :8080")
}

func main() {
	hub_main()
	pub_main()
	sub_main()
	<-make(chan struct{})
}
