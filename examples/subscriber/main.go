package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/notnotquinn/go-websub"
)

// To try this example, use something like ngrok (free)
// to serve your local network over the internet.

// $ ngrok http 3033
// > You will get a public URL pointing to your local port 3033

func main() {
	s := websub.NewSubscriber("http://public.example.com/")

	go http.ListenAndServe("127.0.0.1:3033", s)
	fmt.Println("Listening on 127.0.0.1:3033")

	_, err := s.Subscribe(
		// Topic URL that exposes Link headers for "hub" and "self".
		"https://websub.rocks/blog/302/dys2HMONR3vGl5SjtbQj",
		// for authenticated content distribution (maximum of 200 characters)
		"random bytes",
		// Callback function is called when the subscriber receives a valid
		// request from the hub, not on invalid ones
		// (for example ones with a missing or invalid hub signature)
		func(sub *websub.SSubscription, contentType string, body io.Reader) {
			// Note: With this topic URL,
			// I don't think its possible to receive a publish.
			fmt.Println("Received publish!")
		},
	)

	if err != nil {
		panic(err) // handle
	}

	<-make(chan struct{})
}
