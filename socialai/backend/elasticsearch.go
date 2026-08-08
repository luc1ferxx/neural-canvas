package backend

import (
	"context"
	"fmt"

	"socialai/config"
	"socialai/constants"

	"github.com/olivere/elastic/v7"
)

var (
	ESBackend *ElasticsearchBackend
)

type ElasticsearchBackend struct {
	client *elastic.Client
}

const postMapping = `{
    "mappings": {
        "properties": {
            "id":      { "type": "keyword" },
            "user":    { "type": "keyword" },
            "message": { "type": "text" },
            "url":     { "type": "keyword", "index": false },
            "type":    { "type": "keyword", "index": false }
        }
    }
}`

// userMapping stores "password" as a bcrypt hash, never the password itself.
// The field is not indexed: nothing should ever query by it. Authentication
// looks the user up by username and compares the hash in application code.
const userMapping = `{
    "mappings": {
        "properties": {
            "username": { "type": "keyword" },
            "password": { "type": "keyword", "index": false },
            "age":      { "type": "long",    "index": false },
            "gender":   { "type": "keyword", "index": false }
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

	ESBackend = &ElasticsearchBackend{client: client}
	return nil
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
