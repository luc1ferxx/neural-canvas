package handler

import (
	"log/slog"
	"net/http"

	jwtMiddleware "github.com/auth0/go-jwt-middleware"
	jwt "github.com/form3tech-oss/jwt-go"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/logging"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func InitRouter() http.Handler {
	jwtMw := jwtMiddleware.New(jwtMiddleware.Options{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			return config.C.JWTSecret, nil
		},
		SigningMethod: jwt.SigningMethodHS256,

		// Without this the library's default handler answers with a bare
		// text/plain body ("Required authorization token not found"), which would
		// leave the single most common error in the API -- a missing or expired
		// token -- as the one response a JSON client cannot parse.
		//
		// err carries the library's own description. It is logged rather than
		// returned: the distinction between "no token", "malformed token" and
		// "bad signature" is useful in a log and is a probing aid to a caller.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err string) {
			logging.FromContext(r.Context()).Debug("token rejected",
				slog.String("cause", err))
			writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
		},
	})

	// protected requires a validly signed token AND a session that has not been
	// revoked by a sign-out. Both checks belong on every authenticated route, so
	// they are composed here rather than repeated per handler.
	protected := func(h http.HandlerFunc) http.Handler {
		return jwtMw.Handler(requireLiveSession(h))
	}

	router := mux.NewRouter()

	// Probes are unauthenticated and outside the JWT middleware: a load balancer
	// has no token, and requiring one would make every instance permanently
	// unready.
	router.Handle(healthzPath, http.HandlerFunc(healthzHandler)).Methods("GET")
	router.Handle(readyzPath, http.HandlerFunc(readyzHandler)).Methods("GET")

	router.Handle("/upload", protected(uploadHandler)).Methods("POST")
	router.Handle("/search", protected(searchHandler)).Methods("GET")
	router.Handle("/generate", protected(generateHandler)).Methods("POST")
	router.Handle("/post/{id}", protected(deleteHandler)).Methods("DELETE")
	router.Handle("/signout", protected(signoutHandler)).Methods("POST")

	router.Handle("/signup", http.HandlerFunc(signupHandler)).Methods("POST")
	router.Handle("/signin", http.HandlerFunc(signinHandler)).Methods("POST")

	// Explicit origins from config, never "*". These endpoints require an
	// Authorization header, so a wildcard would let any site drive the API on
	// behalf of a user whose token it managed to obtain.
	originsOk := handlers.AllowedOrigins(config.C.AllowedOrigins)
	headersOk := handlers.AllowedHeaders([]string{"Authorization", "Content-Type", requestIDHeader})
	methodsOk := handlers.AllowedMethods([]string{"GET", "POST", "DELETE"})
	// Exposed so a browser client can read the id off a failed response and
	// include it in a bug report; without this the header is invisible to JS.
	exposedOk := handlers.ExposedHeaders([]string{requestIDHeader})

	// Ordering, outermost first:
	//
	//   CORS          rejects a disallowed origin before any work is done
	//   withRequestID every entry below this point carries the id, including the
	//                 access log and any panic
	//   accessLog     outside recoverPanic, so a panic is still recorded as the
	//                 500 the client actually received
	//   recoverPanic  closest to the handlers, which are what can panic
	return handlers.CORS(originsOk, headersOk, methodsOk, exposedOk)(
		withRequestID(accessLog(recoverPanic(router))),
	)
}
