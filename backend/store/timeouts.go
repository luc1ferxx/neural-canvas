package store

import "time"

// Timeouts for outbound calls.
//
// Every store method derives its deadline from the caller's context with
// context.WithTimeout, which fires at whichever comes first: the caller's
// deadline or this bound. That composition is the point -- a client that hangs
// up cancels the query immediately via the request context, and a dependency
// that has stopped answering is abandoned after a known interval instead of
// pinning a goroutine and a connection indefinitely.
//
// The values differ by an order of magnitude because the operations do. A single
// timeout applied uniformly would have to be as generous as the slowest one, and
// a 5-minute deadline on a keyword lookup is not a timeout, it is a formality.
const (
	// esReadTimeout covers a get-by-id or a search. These are sub-millisecond
	// lookups at this scale; a second is already pathological.
	esReadTimeout = 3 * time.Second

	// esWriteTimeout is larger than the read budget because writes use
	// Refresh("wait_for"), which deliberately blocks until the document is
	// searchable -- about one refresh interval, and longer under indexing load.
	esWriteTimeout = 15 * time.Second

	// esAdminTimeout covers index creation and mapping checks at startup, where a
	// cold cluster is slower than a warm one.
	esAdminTimeout = 30 * time.Second

	// gcsUploadTimeout has to cover streaming an upload capped at 32 MiB from a
	// client on an arbitrarily slow connection. This is a backstop against a
	// stalled transfer, not a performance target.
	gcsUploadTimeout = 5 * time.Minute

	// gcsDeleteTimeout covers a single object delete.
	gcsDeleteTimeout = 30 * time.Second
)
