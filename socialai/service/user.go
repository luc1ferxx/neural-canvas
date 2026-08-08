package service

import (
    "fmt"
    "reflect"

    "socialai/backend"
    "socialai/constants"
    "socialai/model"

    "github.com/olivere/elastic/v7"
    "golang.org/x/crypto/bcrypt"
)

// ErrUserExists reports a taken username.
var ErrUserExists = fmt.Errorf("user already exists")

// CheckUser looks the user up by username and compares the supplied password
// against the stored bcrypt hash.
//
// The password is never part of the query. The previous implementation searched
// Elasticsearch for username AND password as a term query, which only works if
// the password is stored in plaintext.
//
// Returns (false, nil) for both "no such user" and "wrong password" so the
// caller cannot distinguish them and leak which usernames exist.
func CheckUser(username, password string) (bool, error) {
    query := elastic.NewTermQuery("username", username)

    searchResult, err := backend.ESBackend.ReadFromES(query, constants.USER_INDEX)
    if err != nil {
        return false, err
    }

    users := getUserFromSearchResult(searchResult)
    if len(users) == 0 {
        // Hash a throwaway value so a missing user costs roughly the same time
        // as a wrong password; otherwise response latency reveals which
        // usernames are registered.
        _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
        return false, nil
    }

    stored := users[0].Password
    if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)); err != nil {
        if err == bcrypt.ErrMismatchedHashAndPassword {
            return false, nil
        }
        // A malformed hash means this row predates hashing. Those rows are not
        // usable for login by design: see the migration note in README.
        return false, fmt.Errorf("stored credential for %q is not a valid bcrypt hash: %w", username, err)
    }

    return true, nil
}

// dummyHash is a valid bcrypt hash of a value nobody can supply, used purely to
// equalize timing on the user-not-found path.
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// AddUser hashes the password and stores the user. Returns ErrUserExists if the
// username is taken.
func AddUser(user *model.User) error {
    query := elastic.NewTermQuery("username", user.Username)
    searchResult, err := backend.ESBackend.ReadFromES(query, constants.USER_INDEX)
    if err != nil {
        return err
    }
    if searchResult.TotalHits() > 0 {
        return ErrUserExists
    }

    hashed, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
    if err != nil {
        return fmt.Errorf("hash password: %w", err)
    }

    // Store a copy so the caller's struct never carries the hash back out, and
    // so the plaintext in user.Password is not what gets serialized.
    toStore := *user
    toStore.Password = string(hashed)

    if err := backend.ESBackend.SaveToES(&toStore, constants.USER_INDEX, toStore.Username); err != nil {
        return err
    }

    fmt.Printf("User is added: %s\n", user.Username)
    return nil
}

func getUserFromSearchResult(searchResult *elastic.SearchResult) []model.User {
    var utype model.User
    var users []model.User

    for _, item := range searchResult.Each(reflect.TypeOf(utype)) {
        if u, ok := item.(model.User); ok {
            users = append(users, u)
        }
    }
    return users
}
