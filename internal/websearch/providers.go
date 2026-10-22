package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type tavilyProvider struct {
	base string
	key  string
	http *http.Client
}

type tavilyResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

func (p *tavilyProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	body, err := postJSON(ctx, p.http, p.base+"/search", map[string]any{
		"api_key":     p.key,
		"query":       query,
		"max_results": limit,
	}, nil)
	if err != nil {
		return nil, err
	}
	var parsed tavilyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	out := make([]Result, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		out = append(out, Result{Title: item.Title, URL: item.URL, Snippet: item.Content})
	}
	return out, nil
}

type bochaProvider struct {
	base string
	key  string
	http *http.Client
}

type bochaResponse struct {
	Data struct {
		WebPages struct {
			Value []struct {
				Name            string `json:"name"`
				URL             string `json:"url"`
				Snippet         string `json:"snippet"`
				SiteName        string `json:"siteName"`
				DateLastCrawled string `json:"dateLastCrawled"`
			} `json:"value"`
		} `json:"webPages"`
	} `json:"data"`
}

func (p *bochaProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	body, err := postJSON(ctx, p.http, p.base+"/web-search", map[string]any{
		"query":   query,
		"count":   limit,
		"summary": true,
	}, map[string]string{"Authorization": "Bearer " + p.key})
	if err != nil {
		return nil, err
	}
	var parsed bochaResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	out := make([]Result, 0, len(parsed.Data.WebPages.Value))
	for _, item := range parsed.Data.WebPages.Value {
		out = append(out, Result{
			Title:       item.Name,
			URL:         item.URL,
			Snippet:     item.Snippet,
			Site:        item.SiteName,
			PublishedAt: item.DateLastCrawled,
		})
	}
	return out, nil
}

type braveProvider struct {
	base string
	key  string
	http *http.Client
}

type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
			Age         string `json:"age"`
			MetaURL     struct {
				Hostname string `json:"hostname"`
			} `json:"meta_url"`
		} `json:"results"`
	} `json:"web"`
}

func (p *braveProvider) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	endpoint := fmt.Sprintf("%s/web/search?q=%s&count=%d", p.base, url.QueryEscape(query), limit)
	body, err := getJSON(ctx, p.http, endpoint, map[string]string{
		"X-Subscription-Token": p.key,
		"Accept":               "application/json",
	})
	if err != nil {
		return nil, err
	}
	var parsed braveResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	out := make([]Result, 0, len(parsed.Web.Results))
	for _, item := range parsed.Web.Results {
		out = append(out, Result{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Description,
			Site:        item.MetaURL.Hostname,
			PublishedAt: item.Age,
		})
	}
	return out, nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, payload map[string]any, headers map[string]string) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return doRequest(client, req)
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return doRequest(client, req)
}

// doRequest reads a response and reports status/code errors without echoing the
// request (which may carry the API key) or the provider body verbatim.
func doRequest(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return nil, fmt.Errorf("search provider returned HTTP %d: %s", resp.StatusCode, detail)
	}
	return body, nil
}
