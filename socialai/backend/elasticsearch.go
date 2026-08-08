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

func (backend *ElasticsearchBackend) DeleteFromES(query elastic.Query, index string) error {
	_, err := backend.client.DeleteByQuery().
		Index(index).
		Query(query).
		Pretty(true).
		Do(context.Background())

	return err
}

func (backend *ElasticsearchBackend) SaveToES(i interface{}, index string, id string) error {
	_, err := backend.client.Index().
		Index(index).
		Id(id).
		BodyJson(i).
		Do(context.Background())
	return err
}
