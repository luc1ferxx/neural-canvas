package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/constants"

	"github.com/olivere/elastic/v7"
)

var (
	ESBackend *ElasticsearchBackend
)

type ElasticsearchBackend struct {
	client *elastic.Client
}

// postMapping indexes "type" so posts can be filtered by it server-side. The
// original mapping used "index": false here, which is why this index is
// versioned -- see constants.POST_INDEX.
//
// "url" stays unindexed: it is only ever read back, never queried.
const postMapping = `{
    "mappings": {
        "properties": {
            "id":      { "type": "keyword" },
            "user":    { "type": "keyword" },
            "message": { "type": "text" },
            "url":     { "type": "keyword", "index": false },
            "type":    { "type": "keyword" }
        }
    }
}`

// userMapping stores "password" as a bcrypt hash, never the password itself.
// The field is not indexed: nothing should ever query by it. Authentication
// looks the user up by username and compares the hash in application code.
//
// tokensValidAfter is a unix timestamp used to revoke sessions: a token whose
// "iat" predates it is refused. See handler/revocation.go.
const userMapping = `{
    "mappings": {
        "properties": {
            "username":         { "type": "keyword" },
            "password":         { "type": "keyword", "index": false },
            "age":              { "type": "long",    "index": false },
            "gender":           { "type": "keyword", "index": false },
            "tokensValidAfter": { "type": "long",    "index": false }
        }
    }
}`

// loginAttemptMapping stores failed sign-in counters. Nothing queries these
// fields; they are read by document id.
const loginAttemptMapping = `{
    "mappings": {
        "properties": {
            "failures":     { "type": "long", "index": false },
            "firstAttempt": { "type": "long", "index": false }
        }
    }
}`

// InitElasticsearchBackend connects and ensures both indexes exist. It returns
// an error rather than panicking so main can report a usable message and exit.
func InitElasticsearchBackend() error {
	client, err := elastic.NewClient(
		elastic.SetURL(config.C.ESURL),
		elastic.SetBasicAuth(config.C.ESUsername, config.C.ESPassword),
		// Sniffing asks the cluster for its node list and then talks to the
		// addresses it reports. Behind a managed endpoint or a NAT those
		// addresses are unreachable from here, and the client fails at startup.
		elastic.SetSniff(false),
	)
	if err != nil {
		return fmt.Errorf("connect to elasticsearch at %s: %w", config.C.ESURL, err)
	}

	if err := ensureIndex(client, constants.POST_INDEX, postMapping); err != nil {
		return err
	}
	if err := ensureIndex(client, constants.USER_INDEX, userMapping); err != nil {
		return err
	}
	if err := ensureIndex(client, constants.LOGIN_ATTEMPT_INDEX, loginAttemptMapping); err != nil {
		return err
	}

	ESBackend = &ElasticsearchBackend{client: client}

	warnIfMigrationPending(ESBackend)

	return nil
}

// warnIfMigrationPending reports the case where the legacy post index still holds
// documents but the current one is empty.
//
// That combination means an upgrade happened without running the reindex, and the
// symptom is alarming and easy to misread: every post appears to have vanished.
// The data is intact in the old index, so say so loudly rather than letting an
// operator discover it through an empty gallery.
func warnIfMigrationPending(es *ElasticsearchBackend) {
	legacyExists, err := es.IndexExists(constants.POST_INDEX_LEGACY)
	if err != nil || !legacyExists {
		return
	}

	legacyCount, err := es.CountDocuments(constants.POST_INDEX_LEGACY)
	if err != nil || legacyCount == 0 {
		return
	}

	currentCount, err := es.CountDocuments(constants.POST_INDEX)
	if err != nil || currentCount > 0 {
		return
	}

	fmt.Fprintf(os.Stderr, `
=============================================================================
MIGRATION PENDING

  %q holds %d document(s) but %q is empty.

  Posts will not appear until they are copied across. The old data is intact;
  nothing has been deleted. Run:

      go run ./cmd/reindex

  The index was renamed because the original mapping declared "type" with
  "index": false, which makes it unsearchable, and that cannot be changed on
  an existing field.
=============================================================================

`, constants.POST_INDEX_LEGACY, legacyCount, constants.POST_INDEX)
}

func ensureIndex(client *elastic.Client, index, mapping string) error {
	exists, err := client.IndexExists(index).Do(context.Background())
	if err != nil {
		return fmt.Errorf("check index %q: %w", index, err)
	}
	if exists {
		return nil
	}
	if _, err := client.CreateIndex(index).Body(mapping).Do(context.Background()); err != nil {
		return fmt.Errorf("create index %q: %w", index, err)
	}
	return nil
}

// PostMapping exposes the post mapping to the reindex command.
func PostMapping() string { return postMapping }

// IndexExists reports whether an index is present.
func (backend *ElasticsearchBackend) IndexExists(index string) (bool, error) {
	return backend.client.IndexExists(index).Do(context.Background())
}

// CountDocuments returns the number of documents in an index.
func (backend *ElasticsearchBackend) CountDocuments(index string) (int64, error) {
	return backend.client.Count(index).Do(context.Background())
}

// EnsureIndex creates an index with the given mapping if it does not exist.
func (backend *ElasticsearchBackend) EnsureIndex(index, mapping string) error {
	return ensureIndex(backend.client, index, mapping)
}

// DeleteIndex removes an index entirely. Used by tests to clean up.
func (backend *ElasticsearchBackend) DeleteIndex(index string) error {
	_, err := backend.client.DeleteIndex(index).Do(context.Background())
	if err != nil {
		if elastic.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete index %q: %w", index, err)
	}
	return nil
}

// Reindex copies every document from src into dst and returns how many were
// written. It waits for the copy to finish and refreshes dst so the documents
// are immediately searchable.
func (backend *ElasticsearchBackend) Reindex(src, dst string) (int64, error) {
	resp, err := backend.client.Reindex().
		SourceIndex(src).
		DestinationIndex(dst).
		Refresh("true").
		Do(context.Background())
	if err != nil {
		return 0, fmt.Errorf("reindex %q into %q: %w", src, dst, err)
	}
	if resp == nil {
		return 0, nil
	}
	return resp.Created + resp.Updated, nil
}

// ReadFromES runs query and returns at most Elasticsearch's default of 10 hits.
// Use it only for lookups that expect a single document, such as resolving a
// username or checking ownership of one post. For anything list-shaped use
// ReadFromESPaged: the default was silently capping search results at 10.
func (backend *ElasticsearchBackend) ReadFromES(query elastic.Query, index string) (*elastic.SearchResult, error) {
	searchResult, err := backend.client.Search().
		Index(index).
		Query(query).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return nil, err
	}

	return searchResult, nil
}

// ReadFromESPaged runs query with an explicit window. Callers must pass a size;
// leaving it unset is what limited every search to 10 results.
func (backend *ElasticsearchBackend) ReadFromESPaged(query elastic.Query, index string, from, size int) (*elastic.SearchResult, error) {
	searchResult, err := backend.client.Search().
		Index(index).
		Query(query).
		From(from).
		Size(size).
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return nil, err
	}

	return searchResult, nil
}

// DeleteFromES removes every document matching query and reports how many were
// deleted, so a caller can tell "nothing matched" from "deleted successfully"
// instead of reporting success for a no-op.
//
// Refresh("true") makes the deletion visible to the next search. Unlike the
// single-document delete API, _delete_by_query does not accept "wait_for".
func (backend *ElasticsearchBackend) DeleteFromES(query elastic.Query, index string) (int64, error) {
	resp, err := backend.client.DeleteByQuery().
		Index(index).
		Query(query).
		Refresh("true").
		Pretty(true).
		Do(context.Background())
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, nil
	}

	return resp.Deleted, nil
}

// GetDocument fetches one document by id and unmarshals its _source into out.
// Reports whether the document exists.
//
// Get by id is realtime in Elasticsearch: unlike search it does not wait for a
// refresh, so a value written a moment ago is visible immediately. That property
// is what makes it the right primitive for authentication and for session
// revocation checks.
func (backend *ElasticsearchBackend) GetDocument(index, id string, out interface{}) (bool, error) {
	resp, err := backend.client.Get().
		Index(index).
		Id(id).
		Do(context.Background())
	if err != nil {
		if elastic.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get %q from %q: %w", id, index, err)
	}
	if resp == nil || !resp.Found || resp.Source == nil {
		return false, nil
	}

	if err := json.Unmarshal(resp.Source, out); err != nil {
		return false, fmt.Errorf("decode %q from %q: %w", id, index, err)
	}
	return true, nil
}

// UpdateFields applies a partial update, leaving fields it does not mention
// untouched. Used so revoking a session does not have to rewrite the password
// hash.
func (backend *ElasticsearchBackend) UpdateFields(index, id string, fields map[string]interface{}) error {
	_, err := backend.client.Update().
		Index(index).
		Id(id).
		Doc(fields).
		Do(context.Background())
	if err != nil {
		return fmt.Errorf("update %q in %q: %w", id, index, err)
	}
	return nil
}

// UpdateWithScript runs a Painless script against one document, creating it from
// upsert if it does not exist yet.
//
// The script runs inside Elasticsearch, so a read-modify-write is not needed and
// concurrent callers cannot lose each other's increments. RetryOnConflict covers
// the case where two requests touch the same document in the same instant.
func (backend *ElasticsearchBackend) UpdateWithScript(
	index, id, script string,
	params map[string]interface{},
	upsert map[string]interface{},
) error {
	_, err := backend.client.Update().
		Index(index).
		Id(id).
		Script(elastic.NewScript(script).Lang("painless").Params(params)).
		Upsert(upsert).
		RetryOnConflict(3).
		Do(context.Background())
	if err != nil {
		return fmt.Errorf("scripted update %q in %q: %w", id, index, err)
	}
	return nil
}

// DeleteDocument removes one document by id. A missing document is not an error.
func (backend *ElasticsearchBackend) DeleteDocument(index, id string) error {
	_, err := backend.client.Delete().
		Index(index).
		Id(id).
		Do(context.Background())
	if err != nil {
		if elastic.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete %q from %q: %w", id, index, err)
	}
	return nil
}

// SaveToES indexes a document and waits for it to become searchable.
//
// Refresh("wait_for") is what lets a caller read its own write. Elasticsearch
// refreshes about once a second by default, so without this a newly created post
// is invisible to the search that immediately follows it -- which the frontend
// used to paper over with a three-second sleep -- and a fresh signup could fail
// the login that follows it.
//
// The cost is up to one refresh interval of latency on the write. That is
// preferable to a fixed client-side delay that is simultaneously too slow in the
// common case and still a race in the worst one.
func (backend *ElasticsearchBackend) SaveToES(i interface{}, index string, id string) error {
	_, err := backend.client.Index().
		Index(index).
		Id(id).
		BodyJson(i).
		Refresh("wait_for").
		Do(context.Background())
	return err
}
