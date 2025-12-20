package runner

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// A failing transport should be reported in the case result rather than aborting the run.
func TestExecuteParsedReportsHTTPError(t *testing.T) {
	parsed := parsedFile{
		FilePath: "f.bru",
		Meta:     metaBlock{Name: "Net fail"},
		Request:  requestBlock{Verb: "GET", URL: "http://example.invalid"},
	}

	g, _ := New(context.Background())
	r := g.(*runner)

	opts := RunOptions{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial tcp: refused")
			}),
		},
	}

	res, err := r.executeParsed(context.Background(), parsed, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected failure result")
	}
	if res.ErrorText == "" || res.ErrorText == "err" {
		t.Fatalf("expected informative error, got %q", res.ErrorText)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
