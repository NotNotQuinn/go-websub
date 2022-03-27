package main

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/notnotquinn/go-websub"
)

func main() {
	s := websub.NewSubscriber("http://public.example.com/")

	go http.ListenAndServe("127.0.0.1:3033", s)
	fmt.Println("Listening on 127.0.0.1:3033")

	topicUrl := "https://websub.rocks/blog/302/dys2HMONR3vGl5SjtbQj"
	secret := "secret"

	_, err := s.Subscribe(topicUrl, secret, func(sub *websub.SSubscription, contentType string, body io.Reader) {
		io.Copy(os.Stdout, body)
	})

	if err != nil {
		panic(err)
	}

	<-make(chan struct{})
}
