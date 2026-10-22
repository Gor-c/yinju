package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAvailableModels(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":[{"id":"glm-5.2","context_length":1048576,"supported_features":["tools"]},{"id":"kimi-k3","name":"Kimi K3"}]}`)
	}))
	defer srv.Close()

	s := &Server{http: srv.Client()}
	r := testRouter(s)
	w := doRequest(r, http.MethodPost, "/api/v1/settings/models/list",
		`{"kind":"llm","base_url":"`+srv.URL+`/v1/chat/completions","api_key":"sk-test"}`, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/models" {
		t.Fatalf("upstream path = %q, want /v1/models (base url should be normalized)", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	body := w.Body.String()
	for _, want := range []string{`"count":2`, `"glm-5.2"`, `"kimi-k3"`, `"Kimi K3"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestListAvailableModelsValidation(t *testing.T) {
	s := &Server{http: http.DefaultClient}
	r := testRouter(s)

	cases := []struct {
		name, body string
		want       int
	}{
		{"bad kind", `{"kind":"image"}`, http.StatusBadRequest},
		{"missing url", `{"kind":"llm"}`, http.StatusBadRequest},
		{"missing key", `{"kind":"llm","base_url":"https://example.com/v1"}`, http.StatusBadRequest},
		{"bad url", `{"kind":"embed","base_url":"example.com/v1","api_key":"k"}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		w := doRequest(r, http.MethodPost, "/api/v1/settings/models/list", c.body, nil)
		if w.Code != c.want {
			t.Errorf("%s: status = %d, want %d, body = %s", c.name, w.Code, c.want, w.Body.String())
		}
	}
}

func TestListAvailableModelsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid api key"}`)
	}))
	defer srv.Close()

	s := &Server{http: srv.Client()}
	r := testRouter(s)
	w := doRequest(r, http.MethodPost, "/api/v1/settings/models/list",
		`{"kind":"llm","base_url":"`+srv.URL+`","api_key":"bad"}`, nil)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "401") || !strings.Contains(w.Body.String(), "invalid api key") {
		t.Fatalf("body should carry upstream detail: %s", w.Body.String())
	}
}
