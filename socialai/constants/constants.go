package constants

// Index names are the only genuine constants left here. Everything that used to
// live in this file (Elasticsearch URL and credentials, GCS bucket) is now read
// from the environment by package config -- it does not belong in git.
const (
	USER_INDEX = "user"
	POST_INDEX = "post"
)
