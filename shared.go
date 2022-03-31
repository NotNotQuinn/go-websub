package websub

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"hash"

	"github.com/rs/zerolog"
)

var (
	log = zerolog.New(zerolog.NewConsoleWriter())
)

// Logger returns the logger the websub package uses
func Logger() zerolog.Logger {
	return log
}

// calculates the hash using one of "sha1", "sha256", "sha384", or "sha512".
//
// If an unrecognized hash function is passed, "sha1" is used for compatability,
// and a warning is printed to the console.
func calculateHash(hashFunction_, secret string, content []byte) (hashResult string, hashFunction string) {
	var hasher func() hash.Hash
	hashFunction = hashFunction_

	switch hashFunction_ {
	case "sha1":
		hasher = sha1.New
	case "sha256":
		hasher = sha256.New
	case "sha384":
		hasher = sha512.New384
	case "sha512":
		hasher = sha512.New
	default:
		log.Warn().
			Str("hashFunction", hashFunction_).
			Msg("hash function not recognized, using sha1")
		hashFunction = "sha1"
		hasher = sha1.New
	}

	mac := hmac.New(hasher, []byte(secret))
	mac.Write(content)
	hashResult = hex.EncodeToString(mac.Sum(nil))
	return hashResult, hashFunction
}
