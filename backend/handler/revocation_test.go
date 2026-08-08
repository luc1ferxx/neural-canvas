package handler

import (
	"encoding/json"
	"testing"

	jwt "github.com/form3tech-oss/jwt-go"
)

// TestIssuedAtFromClaims covers the representations an iat claim can arrive in.
// JSON numbers decode to float64 by default, so asserting straight to int64
// would fail on every real token.
func TestIssuedAtFromClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims jwt.MapClaims
		want   int64
		wantOK bool
	}{
		{"float64 as produced by encoding/json", jwt.MapClaims{"iat": float64(1700000000)}, 1700000000, true},
		{"int64", jwt.MapClaims{"iat": int64(1700000000)}, 1700000000, true},
		{"int", jwt.MapClaims{"iat": 1700000000}, 1700000000, true},
		{"json.Number", jwt.MapClaims{"iat": json.Number("1700000000")}, 1700000000, true},

		{"missing", jwt.MapClaims{}, 0, false},
		{"string", jwt.MapClaims{"iat": "1700000000"}, 0, false},
		{"nil", jwt.MapClaims{"iat": nil}, 0, false},
		{"bool", jwt.MapClaims{"iat": true}, 0, false},
		{"unparseable json.Number", jwt.MapClaims{"iat": json.Number("not-a-number")}, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := issuedAtFromClaims(tc.claims)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("iat = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestIssuedAtRoundTripsThroughRealJSON confirms the float64 path is the one a
// genuine token exercises.
func TestIssuedAtRoundTripsThroughRealJSON(t *testing.T) {
	encoded, err := json.Marshal(map[string]interface{}{
		"username": "tester",
		"iat":      1700000000,
		"exp":      1700086400,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded jwt.MapClaims
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	got, ok := issuedAtFromClaims(decoded)
	if !ok {
		t.Fatal("iat not readable from claims decoded from real JSON")
	}
	if got != 1700000000 {
		t.Errorf("iat = %d, want 1700000000", got)
	}
}
