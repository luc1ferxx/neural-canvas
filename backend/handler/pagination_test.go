package handler

import (
	"net/url"
	"testing"
)

func TestParsePagination(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantFrom int
		wantSize int
	}{
		// The regression this guards: no size parameter must not mean
		// Elasticsearch's default of 10.
		{"no params", "", 0, defaultPageSize},

		{"explicit values", "from=20&size=30", 20, 30},
		{"size only", "size=5", 0, 5},
		{"from only", "from=15", 15, defaultPageSize},

		// Clamping: a bad page parameter should not fail the search.
		{"size over max", "size=99999", 0, maxPageSize},
		{"negative size", "size=-5", 0, defaultPageSize},
		{"zero size", "size=0", 0, defaultPageSize},
		{"negative from", "from=-10", 0, defaultPageSize},
		{"non-numeric", "from=abc&size=xyz", 0, defaultPageSize},
		{"empty values", "from=&size=", 0, defaultPageSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tc.query, err)
			}

			from, size := parsePagination(q)
			if from != tc.wantFrom {
				t.Errorf("from = %d, want %d", from, tc.wantFrom)
			}
			if size != tc.wantSize {
				t.Errorf("size = %d, want %d", size, tc.wantSize)
			}
		})
	}
}

func TestParsePaginationNeverExceedsMax(t *testing.T) {
	// Property: whatever the input, size stays within (0, maxPageSize].
	for _, raw := range []string{"1", "200", "201", "100000", "-1", "0", "abc", ""} {
		q := url.Values{"size": []string{raw}}
		_, size := parsePagination(q)
		if size <= 0 || size > maxPageSize {
			t.Errorf("size=%q produced %d, outside (0, %d]", raw, size, maxPageSize)
		}
	}
}
