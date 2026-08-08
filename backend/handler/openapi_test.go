package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gorilla/mux"
)

// specPath is relative to this package, which is where `go test` runs.
const specPath = "../openapi.yaml"

// templateParams matches an OpenAPI or mux path parameter, so a template can be
// turned into a concrete path that the router will actually match.
var templateParams = regexp.MustCompile(`\{[^}]+\}`)

// loadSpec parses openapi.yaml and checks it against the OpenAPI 3 meta-schema.
//
// This alone is worth a test: a spec is a document, nothing refuses to serve
// traffic when it is malformed, and the usual way to find out is that somebody's
// client generator fails months later.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()

	loader := &openapi3.Loader{
		Context: context.Background(),
		// The spec is one self-contained file. Allowing external refs would let a
		// $ref reach the filesystem or the network from a test.
		IsExternalRefsAllowed: false,
	}

	doc, err := loader.LoadFromFile(filepath.Clean(specPath))
	if err != nil {
		t.Fatalf("could not load %s: %v", specPath, err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		t.Fatalf("%s is not a valid OpenAPI 3 document: %v", specPath, err)
	}
	return doc
}

// route is one (method, path template) pair, in the form both the router and the
// spec can be reduced to.
type route struct {
	method   string
	template string
}

func (r route) String() string { return r.method + " " + r.template }

// registeredRoutes walks the real router.
//
// mux path templates and OpenAPI path templates happen to use the same syntax for
// parameters -- "/post/{id}" in both -- so no translation is needed. If that ever
// stops being true, this is where it would be handled.
func registeredRoutes(t *testing.T) map[route]bool {
	t.Helper()

	out := map[route]bool{}
	err := apiRouter().Walk(func(r *mux.Route, _ *mux.Router, _ []*mux.Route) error {
		template, err := r.GetPathTemplate()
		if err != nil {
			// A route with no path: this router has none, and one appearing would
			// be worth failing on rather than skipping.
			return err
		}
		methods, err := r.GetMethods()
		if err != nil {
			return err
		}
		for _, method := range methods {
			out[route{method: method, template: template}] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("could not walk the router: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the router reported no routes, so this test would pass vacuously")
	}
	return out
}

// documentedRoutes reduces the spec to the same shape.
func documentedRoutes(t *testing.T, doc *openapi3.T) map[route]bool {
	t.Helper()

	out := map[route]bool{}
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			out[route{method: method, template: path}] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("the spec described no operations, so this test would pass vacuously")
	}
	return out
}

// TestEveryRouteIsDocumented is the half that catches a new endpoint shipping
// undocumented, which is the direction that actually happens: adding a route is a
// three-line change and nothing about it prompts anyone to open a YAML file.
func TestEveryRouteIsDocumented(t *testing.T) {
	documented := documentedRoutes(t, loadSpec(t))

	var missing []string
	for r := range registeredRoutes(t) {
		if !documented[r] {
			missing = append(missing, r.String())
		}
	}

	sort.Strings(missing)
	for _, r := range missing {
		t.Errorf("%s is served but not described in %s", r, specPath)
	}
}

// TestEveryDocumentedRouteExists is the other half: the spec promising an endpoint
// the server does not have.
//
// It resolves each documented path against the router rather than comparing
// strings, so a typo in a path parameter name is caught too -- "/post/{postId}"
// would compare unequal to "/post/{id}" as a string, but the interesting question
// is whether a request to that path reaches a handler.
func TestEveryDocumentedRouteExists(t *testing.T) {
	for r := range documentedRoutes(t, loadSpec(t)) {
		// Substitute something concrete for each parameter, so the path can
		// actually be matched.
		concrete := templateParams.ReplaceAllString(r.template, "00000000-0000-4000-8000-000000000000")

		var match mux.RouteMatch
		// Deliberately not the boolean Match returns. Because the router has a
		// NotFoundHandler, mux answers true for a path that matches nothing -- it
		// has found a handler, just not a route. Reading the boolean made this test
		// pass for an endpoint the spec had invented, which is precisely the case it
		// exists to catch; a mutation caught it. Route is the honest signal, and it
		// is nil both for an unknown path and for a known path with the wrong
		// method, which MatchErr then distinguishes.
		apiRouter().Match(httptest.NewRequest(r.method, concrete, nil), &match)

		if match.Route == nil {
			reason := "no route matches that path"
			// errors.Is, not ==: mux assigns this error directly today, but a
			// wrapped one would make == silently false forever, which is exactly the
			// bug the linter found in the storage delete path.
			if errors.Is(match.MatchErr, mux.ErrMethodMismatch) {
				reason = "the path exists but not for that method"
			}
			t.Errorf("%s is documented in %s but %s", r, specPath, reason)
		}
	}
}

// TestDocumentedAuthenticationMatchesTheMiddleware checks the security scheme
// against what the router does, because a spec is the wrong place to be wrong
// about authentication.
//
// A reader who sees no `security` on an operation will build a client that sends
// no token; one who sees `security: []` on an endpoint that in fact requires a
// token has been told the opposite of the truth. Neither mistake shows up in a
// compile, a lint or a functional test -- and the consequence of the inverse
// error, documenting a protected endpoint as public, is that nobody notices the
// endpoint is not actually protected.
func TestDocumentedAuthenticationMatchesTheMiddleware(t *testing.T) {
	doc := loadSpec(t)
	router := newTestRouter(t)

	if len(doc.Security) == 0 {
		t.Fatal("the spec declares no top-level security, so per-operation overrides prove nothing")
	}

	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			// security: [] on the operation opts out of the document default.
			public := op.Security != nil && len(*op.Security) == 0

			concrete := templateParams.ReplaceAllString(path, "00000000-0000-4000-8000-000000000000")
			rec := httptest.NewRecorder()
			// No Authorization header, and no body: the question is only whether the
			// request is turned away for lack of a token, which is decided before a
			// handler ever looks at the body.
			router.ServeHTTP(rec, httptest.NewRequest(method, concrete, nil))

			gotUnauthorized := rec.Code == http.StatusUnauthorized

			switch {
			case public && gotUnauthorized:
				t.Errorf("%s %s is documented as needing no authentication but answered 401",
					method, path)
			case !public && !gotUnauthorized:
				t.Errorf("%s %s is documented as requiring a bearer token but answered %d "+
					"without one; either the route is unprotected or the spec is wrong",
					method, path, rec.Code)
			}
		}
	}
}

// TestErrorCodesInTheSpecMatchTheConstants pins the enum in the spec to the codes
// the handlers can actually emit.
//
// The codes are the part of the error contract clients branch on, so a code that
// exists in the server and not in the spec is an undocumented branch, and one in
// the spec but not the server is a branch a client will write and never reach.
func TestErrorCodesInTheSpecMatchTheConstants(t *testing.T) {
	doc := loadSpec(t)

	schema, ok := doc.Components.Schemas["Error"]
	if !ok || schema.Value == nil {
		t.Fatal("the spec has no Error schema")
	}
	errField, ok := schema.Value.Properties["error"]
	if !ok || errField.Value == nil {
		t.Fatal("the Error schema has no error property")
	}
	codeField, ok := errField.Value.Properties["code"]
	if !ok || codeField.Value == nil {
		t.Fatal("the Error schema has no error.code property")
	}

	documented := map[string]bool{}
	for _, v := range codeField.Value.Enum {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("error.code enum contains a non-string: %v", v)
		}
		documented[s] = true
	}

	// Listed rather than reflected over: Go has no way to enumerate the constants
	// in a const block, so the alternative is no check at all. A new code added
	// without touching this line is caught by the reverse direction below only if
	// it is also missing from the spec -- which is the case worth catching.
	actual := []string{
		codeInvalidRequest, codeUnauthorized, codeSessionRevoked, codeForbidden,
		codeNotFound, codeUserExists, codeUnsupportedType, codePayloadTooLarge,
		codeRateLimited, codeQuotaExceeded, codeInternal, codeUpstreamFailed,
		codeUnavailable,
	}

	for _, code := range actual {
		if !documented[code] {
			t.Errorf("the server can return error code %q, which %s does not document", code, specPath)
		}
		delete(documented, code)
	}

	var extra []string
	for code := range documented {
		extra = append(extra, code)
	}
	sort.Strings(extra)
	for _, code := range extra {
		t.Errorf("%s documents error code %q, which no handler can return", specPath, code)
	}
}

// TestSpecDescribesTheErrorEnvelopeItActuallySends compares the documented
// envelope against a real response, rather than trusting that the YAML describes
// the Go struct.
func TestSpecDescribesTheErrorEnvelopeItActuallySends(t *testing.T) {
	doc := loadSpec(t)
	router := newTestRouter(t)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-endpoint", nil))

	schema, ok := doc.Components.Schemas["Error"]
	if !ok {
		t.Fatal("the spec has no Error schema")
	}

	// VisitJSON walks the actual bytes against the schema, so a renamed or
	// retyped field fails here.
	var body interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the 404 body is not JSON: %v", err)
	}
	if err := schema.Value.VisitJSON(body); err != nil {
		t.Errorf("a real error response does not match the documented Error schema: %v\nbody: %s",
			err, strings.TrimSpace(rec.Body.String()))
	}
}
