// websub provides a WebSub subscriber and publisher.
//
//
package websub

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"

	"github.com/rs/zerolog"
)

var (
	log      = zerolog.New(zerolog.NewConsoleWriter())
	serveMux = http.NewServeMux()

	// BaseURL must only contain scheme and host,
	// eg. "https://example.com/" or without the trailing slash.
	BaseURL = ""

	// User-Agent header when acting as a publisher.
	UserAgentPublisher = "Go-websub-publisher"

	// User-Agent header when acting as a subscriber
	UserAgentSubscriber = "Go-websub-subscriber"
)

// returns the logger for this package
func GetLogger() zerolog.Logger {
	return log
}

// Wraps http.ListenAndServe, using the ServeMux()
func ListenAndServe(addr string) error {
	return http.ListenAndServe(addr, http.HandlerFunc(serveMux.ServeHTTP))
}

// ServeMux returns the ServeMux this package uses,
// and is availible for using a different http server.
func ServeMux() *http.ServeMux {
	return serveMux
}

// randomURL returns a random URL (starting with /)
//
// If non-empty, the prefix is prepended, and prefixed with /.
// For example, if prefix is "websub/subscriptions"
// then "/websub/subscriptions/RANDOM_DATA" will be returned
//
// If the prefix is an empty string, "/RANDOM_DATA" is returned
//
// The URL contains 20 bytes of random data from
// crypto/rand encoded as base64url (without padding).
func randomURL(prefix string) (string, error) {
	randomData := make([]byte, 20)
	_, err := rand.Read(randomData)

	if err != nil {
		log.Error().
			Err(err).
			Msg("could not get random bytes for randomURL")
		return "/", err
	}

	// URL and filename safe variant of Base64
	randomString := base64.RawURLEncoding.EncodeToString(randomData)

	if prefix != "" {
		return "/" + prefix + "/" + randomString, nil
	}

	return "/" + randomString, nil
}
