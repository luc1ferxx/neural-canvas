package service

import (
	"fmt"
	"io"
	"reflect"

	"socialai/backend"
	"socialai/constants"
	"socialai/model"

	"github.com/olivere/elastic/v7"
)

// ErrPostNotFound reports that no post matched the id for this user, either
// because it does not exist or because it belongs to someone else. The two are
// deliberately indistinguishable so the API does not confirm that another
// user's post id exists.
var ErrPostNotFound = fmt.Errorf("post not found")

func SearchPostsByUser(user string, from, size int) ([]model.Post, error) {
	// 1. create a query
	query := elastic.NewTermQuery("user", user)

	// 2. call backend
	searchResult, err := backend.ESBackend.ReadFromESPaged(query, constants.POST_INDEX, from, size)
	if err != nil {
		return nil, err
	}

	return getPostFromSearchResult(searchResult), nil
}

func SearchPostsByKeywords(keywords string, from, size int) ([]model.Post, error) {
	// 1. create a query
	query := elastic.NewMatchQuery("message", keywords)
	query.Operator("AND")
	// An empty keyword lists everything rather than nothing.
	if keywords == "" {
		query.ZeroTermsQuery("all")
	}

	// 2. call backend
	searchResult, err := backend.ESBackend.ReadFromESPaged(query, constants.POST_INDEX, from, size)
	if err != nil {
		return nil, err
	}

	return getPostFromSearchResult(searchResult), nil
}

func getPostFromSearchResult(searchResult *elastic.SearchResult) []model.Post {
	var ptype model.Post
	var posts []model.Post

	for _, item := range searchResult.Each(reflect.TypeOf(ptype)) {
		if p, ok := item.(model.Post); ok {
			posts = append(posts, p)
		}
	}
	return posts
}

// SavePost stores the media in GCS and indexes the post.
//
// contentType must already have been validated against the media allowlist; it
// is applied to the stored object so GCS does not sniff a type at serve time.
func SavePost(post *model.Post, file io.Reader, contentType string) error {
	medialink, err := backend.GCSBackend.SaveToGCS(file, post.Id, contentType)
	if err != nil {
		return err
	}
	post.Url = medialink

	return backend.ESBackend.SaveToES(post, constants.POST_INDEX, post.Id)
}

// DeletePost removes a post and its stored media, but only if it belongs to
// user. Returns ErrPostNotFound when nothing matches.
//
// The media object is deleted before the index entry. Bucket objects are
// publicly readable, so if only one of the two can succeed it must be the file:
// a post row pointing at a missing image is a broken thumbnail, whereas an
// orphaned file remains fetchable by anyone holding the URL.
func DeletePost(id string, user string) error {
	query := elastic.NewBoolQuery()
	query.Must(elastic.NewTermQuery("id", id))
	query.Must(elastic.NewTermQuery("user", user))

	// Confirm ownership first: DeleteByQuery on its own reports success even
	// when it matched nothing, which would tell the user their delete worked
	// when it had not.
	searchResult, err := backend.ESBackend.ReadFromES(query, constants.POST_INDEX)
	if err != nil {
		return err
	}
	if searchResult.TotalHits() == 0 {
		return ErrPostNotFound
	}

	if err := backend.GCSBackend.DeleteFromGCS(id); err != nil {
		return err
	}

	deleted, err := backend.ESBackend.DeleteFromES(query, constants.POST_INDEX)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrPostNotFound
	}

	return nil
}
