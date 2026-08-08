package constants

// Index names are the only genuine constants left here. Everything that used to
// live in this file (Elasticsearch URL and credentials, GCS bucket) is now read
// from the environment by package config -- it does not belong in git.
const (
	USER_INDEX = "user"

	// LOGIN_ATTEMPT_INDEX holds failed sign-in counters, keyed by username. It
	// lives in Elasticsearch rather than in process memory so the limit applies
	// across instances.
	LOGIN_ATTEMPT_INDEX = "login_attempt"

	// POST_INDEX is versioned because the original "post" index mapped the
	// "type" field with "index": false, which makes it unsearchable. That
	// parameter cannot be changed on an existing field, so filtering posts by
	// type server-side requires a new index and a reindex.
	//
	// Run `go run ./cmd/reindex` to copy existing documents across. The type
	// value survives the copy because "index": false still stores the field in
	// _source.
	POST_INDEX = "post_v2"

	// POST_INDEX_LEGACY is the pre-migration index, read only by the reindex
	// command.
	POST_INDEX_LEGACY = "post"
)
