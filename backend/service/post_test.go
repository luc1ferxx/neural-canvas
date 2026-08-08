package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// querySource renders a built query to the JSON that would be sent to
// Elasticsearch, so the filter logic can be asserted on without a cluster.
func querySource(t *testing.T, q PostQuery) string {
	t.Helper()

	src, err := buildPostQuery(q).Source()
	if err != nil {
		t.Fatalf("Source(): %v", err)
	}
	encoded, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	return string(encoded)
}

// TestTypeFilterReachesTheQuery is the regression this migration exists for: the
// type restriction has to be applied by Elasticsearch, not by the browser.
func TestTypeFilterReachesTheQuery(t *testing.T) {
	got := querySource(t, PostQuery{Type: "image", Size: 50})

	if !strings.Contains(got, `"type":"image"`) {
		t.Errorf("query does not filter on type:\n%s", got)
	}
	if !strings.Contains(got, `"filter"`) {
		t.Errorf("type restriction should be a filter clause, not a scoring one:\n%s", got)
	}
}

func TestNoTypeFilterWhenUnset(t *testing.T) {
	got := querySource(t, PostQuery{Size: 50})

	if strings.Contains(got, `"filter"`) {
		t.Errorf("query should have no filter clause when Type is empty:\n%s", got)
	}
}

func TestUserSearchTakesPrecedenceOverKeywords(t *testing.T) {
	got := querySource(t, PostQuery{User: "alice", Keywords: "ignored", Size: 50})

	if !strings.Contains(got, `"user":"alice"`) {
		t.Errorf("query does not restrict to the requested user:\n%s", got)
	}
	if strings.Contains(got, "ignored") {
		t.Errorf("keywords should be ignored when a user is given:\n%s", got)
	}
}

func TestKeywordSearchUsesAndOperator(t *testing.T) {
	got := querySource(t, PostQuery{Keywords: "red bicycle", Size: 50})

	if !strings.Contains(got, `"red bicycle"`) {
		t.Errorf("query does not match the keywords:\n%s", got)
	}
	if !strings.Contains(got, `"operator":"AND"`) {
		t.Errorf("keyword match should require all terms:\n%s", got)
	}
}

// An empty keyword must list everything rather than nothing.
func TestEmptyKeywordsListsEverything(t *testing.T) {
	got := querySource(t, PostQuery{Keywords: "", Size: 50})

	if !strings.Contains(got, `"zero_terms_query":"all"`) {
		t.Errorf("empty keywords should return all posts:\n%s", got)
	}
}

func TestUserAndTypeCombine(t *testing.T) {
	got := querySource(t, PostQuery{User: "bob", Type: "video", Size: 50})

	if !strings.Contains(got, `"user":"bob"`) {
		t.Errorf("missing user restriction:\n%s", got)
	}
	if !strings.Contains(got, `"type":"video"`) {
		t.Errorf("missing type restriction:\n%s", got)
	}
}
