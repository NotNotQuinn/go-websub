package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/notnotquinn/go-websub"
)

var log = websub.GetLogger()

func subscribe_local() {
	fmt.Println("Subscribing!")

	_, err := websub.Subscribe("http://pi1.qdt.home.arpa/", func(body io.Reader) {
		fmt.Println("---  Got event!  ---")
		io.Copy(os.Stdout, body)
		fmt.Println("---              ---")
	})

	if err != nil {
		panic(err)
	}

}

func publish_loop() {
	tick := time.NewTicker(time.Second * 3)
	for {
		pub := websub.NewPublisher("http://localhost:8080/")
		go func() {
			errs := pub.Publish("topicname", "application/json", []byte("{\"lmao\": \"xd\"}"))
			for err := range errs {
				log.Error().Err(err).Msg("Error publishing.")
			}
		}()
		<-tick.C
	}
}

func main() {
	// start web server
	websub.BaseURL = "http://cd2b-200-50-136-69.ngrok.io/"
	go websub.ListenAndServe(":3033")
	fmt.Println("Listening on :3033")

	time.Sleep(time.Second * 5)

	// go publish_loop()
	go subscribe_local()
	// stop on interrupt
	interrupt := make(chan os.Signal, 1)

	signal.Notify(interrupt, os.Interrupt)

	<-interrupt
}
