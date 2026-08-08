package model

type Post struct {
	Id      string `json:"id"`
	User    string `json:"user"`
	Message string `json:"message"`
	Url     string `json:"url"`
	Type    string `json:"type"`
}

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Age      int64  `json:"age"`
	Gender   string `json:"gender"`

	// TokensValidAfter is a unix timestamp. Any token issued before it is
	// refused, which is how signing out revokes a session that would otherwise
	// stay valid for its full 24 hours. Zero means nothing has been revoked.
	TokensValidAfter int64 `json:"tokensValidAfter"`
}
