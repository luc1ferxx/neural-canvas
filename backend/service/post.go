package service

import (
	"context"
	"fmt"
	"io"
	"reflect"

	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/model"
	"github.com/luc1ferxx/neural-canvas/backend/store"

	"github.com/olivere/elastic/v7"
)

// ErrPostNotFound reports that no post matched the id for this user, either
// because it does not exist or because it belongs to someone else. The two are
// deliberately indistinguishable so the API does not confirm that another
// user's post id exists.
var ErrPostNotFound = fmt.Errorf("post not found")

// PostQuery describes a post search. It replaces a pair of functions whose
// parameter lists were growing with every filter.
type PostQuery struct {
	// User restricts results to one author. When set, Keywords is ignored,
	// preserving the original behaviour.
	User string
	// Keywords matches against the post message.
	Keywords string
	// Type filters by "image" or "video". Empty means both.
	Type string

	From int
	Size int
}

// buildPostQuery translates a PostQuery into an Elasticsearch query. Split out
// from SearchPosts so the filter logic can be asserted on without a cluster.
func buildPostQuery(q PostQuery) elastic.Query {
	query := elastic.NewBoolQuery()

	if q.User != "" {
		query.Must(elastic.NewTermQuery("user", q.User))
	} else {
		match := elastic.NewMatchQuery("message", q.Keywords)
		match.Operator("AND")
		// An empty keyword lists everything rather than nothing.
		if q.Keywords == "" {
			match.ZeroTermsQuery("all")
		}
		query.Must(match)
	}

	if q.Type != "" {
		// Filter rather than must: this is a yes/no restriction that should not
		// influence relevance scoring.
		query.Filter(elastic.NewTermQuery("type", q.Type))
	}

	return query
}

// SearchPosts runs a post search.
//
// Filtering by type happens here rather than in the browser. The frontend used
// to fetch one page and split it into tabs client side, so a tab could report
// "No images!" while images existed further down the result set.
func SearchPosts(ctx context.Context, q PostQuery) ([]model.Post, error) {
	searchResult, err := store.ESBackend.ReadFromESPaged(
		ctx, buildPostQuery(q), constants.POST_INDEX, q.From, q.Size)
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
func SavePost(ctx context.Context, post *model.Post, file io.Reader, contentType string) error {
	medialink, err := store.GCSBackend.SaveToGCS(ctx, file, post.Id, contentType)
	if err != nil {
		return err
	}
	post.Url = medialink

	return store.ESBackend.SaveToES(ctx, post, constants.POST_INDEX, post.Id)
}

// DeletePost removes a post and its stored media, but only if it belongs to
// user. Returns ErrPostNotFound when nothing matches.
//
// The media object is deleted before the index entry. Bucket objects are
// publicly readable, so if only one of the two can succeed it must be the file:
// a post row pointing at a missing image is a broken thumbnail, whereas an
// orphaned file remains fetchable by anyone holding the URL.
func DeletePost(ctx context.Context, id string, user string) error {
	query := elastic.NewBoolQuery()
	query.Must(elastic.NewTermQuery("id", id))
	query.Must(elastic.NewTermQuery("user", user))

	// Confirm ownership first: DeleteByQuery on its own reports success even
	// when it matched nothing, which would tell the user their delete worked
	// when it had not.
	searchResult, err := store.ESBackend.ReadFromES(ctx, query, constants.POST_INDEX)
	if err != nil {
		return err
	}
	if searchResult.TotalHits() == 0 {
		return ErrPostNotFound
	}

	if err := store.GCSBackend.DeleteFromGCS(ctx, id); err != nil {
		return err
	}

	deleted, err := store.ESBackend.DeleteFromES(ctx, query, constants.POST_INDEX)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrPostNotFound
	}

	return nil
}
