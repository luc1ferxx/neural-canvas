package handler

import (
	"net/url"
	"strconv"
)

const (
	// defaultPageSize replaces Elasticsearch's implicit default of 10, which was
	// silently truncating every search result set.
	defaultPageSize = 50
	// maxPageSize bounds how much one request can ask for.
	maxPageSize = 200
)

// parsePagination reads "from" and "size" from a query string.
//
// Invalid, negative and over-large values are clamped rather than rejected: a
// malformed page parameter should not fail a search the user asked for.
func parsePagination(q url.Values) (from, size int) {
	from = 0
	size = defaultPageSize

	if raw := q.Get("from"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			from = v
		}
	}

	if raw := q.Get("size"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			size = v
			if size > maxPageSize {
				size = maxPageSize
			}
		}
	}

	return from, size
}
