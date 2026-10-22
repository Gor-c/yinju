package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// DefaultFetchBytes caps how much of a page is downloaded. Pages are archived as
// text, so a very large body is never useful.
const DefaultFetchBytes = 4 << 20

// Page is the readable text extracted from a fetched URL.
type Page struct {
	URL         string
	Title       string
	Text        string
	ContentType string
	Bytes       int
}

var (
	blockedHostErr = errors.New("URL host resolves to a private, loopback or link-local address")
	badSchemeErr   = errors.New("only http and https URLs can be fetched")
)

// Fetch downloads a public URL and returns its readable text. It is guarded
// against SSRF: only public http(s) targets are dialled, redirects are
// re-validated, and the body is capped.
func (c *Client) Fetch(ctx context.Context, rawURL string, maxBytes int64) (Page, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultFetchBytes
	}
	parsed, err := parsePublicURL(rawURL)
	if err != nil {
		return Page{}, err
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			DialContext:           safeDialer(),
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			// A redirect can point back inside the network.
			if _, err := parsePublicURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("Accept", "text/html,text/plain,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "knowledge-base-bot/1.0 (+web-import)")
	resp, err := client.Do(req)
	if err != nil {
		return Page{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Page{}, fmt.Errorf("page returned HTTP %d", resp.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	if !fetchableType(contentType) {
		return Page{}, fmt.Errorf("unsupported content type: %s", contentType)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return Page{}, err
	}
	text := ExtractText(contentType, body)
	if strings.TrimSpace(text) == "" {
		return Page{}, errors.New("page has no readable text")
	}
	return Page{
		URL:         parsed.String(),
		Title:       pageTitle(body, text),
		Text:        text,
		ContentType: contentType,
		Bytes:       len(body),
	}, nil
}

// FetchStandalone fetches a page without requiring a search provider. It is
// used by "save to knowledge base", which only needs the page text.
func FetchStandalone(ctx context.Context, rawURL string, maxBytes int64) (Page, error) {
	client := &Client{}
	return client.Fetch(ctx, rawURL, maxBytes)
}

func fetchableType(contentType string) bool {
	switch contentType {
	case "text/html", "text/plain", "text/markdown", "application/xhtml+xml":
		return true
	}
	return strings.HasPrefix(contentType, "text/")
}

// parsePublicURL rejects non-http(s) URLs and hosts that resolve to addresses
// which are not publicly routable.
func parsePublicURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, badSchemeErr
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("URL host is required")
	}
	ips, err := net.LookupIP(parsed.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("cannot resolve host: %s", parsed.Hostname())
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, blockedHostErr
		}
	}
	return parsed, nil
}

// safeDialer rejects the connection if the resolved address is not public. It
// complements parsePublicURL: DNS may return a different address at dial time.
func safeDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		if target := net.ParseIP(host); target != nil {
			if isBlockedIP(target) {
				return nil, blockedHostErr
			}
			return dialer.DialContext(ctx, network, addr)
		}
		ips, err := net.LookupIP(host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("cannot resolve host: %s", host)
		}
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return nil, blockedHostErr
			}
		}
		return dialer.DialContext(ctx, network, addr)
	}
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	// Carrier-grade NAT (100.64/10) is not covered by IsPrivate.
	cgnat := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	if cgnat.Contains(ip) {
		return true
	}
	return false
}

var (
	// RE2 has no backreferences, so the closing tags are listed explicitly.
	scriptRe    = regexp.MustCompile(`(?is)<(?:script|style|noscript|svg|template)\b[^>]*>.*?</(?:script|style|noscript|svg|template)\s*>`)
	commentRe   = regexp.MustCompile(`(?s)<!--.*?-->`)
	tagRe       = regexp.MustCompile(`(?s)<[^>]+>`)
	blockRe     = regexp.MustCompile(`(?is)</(p|div|br|li|tr|h[1-6]|section|article)>`)
	titleRe     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	spaceRe     = regexp.MustCompile(`[ \t\xa0]+`)
	blankLineRe = regexp.MustCompile(`\n{3,}`)
)

// ExtractText turns an HTML or text body into plain readable text.
func ExtractText(contentType string, body []byte) string {
	raw := string(body)
	if strings.Contains(contentType, "html") {
		raw = commentRe.ReplaceAllString(raw, " ")
		raw = scriptRe.ReplaceAllString(raw, " ")
		raw = blockRe.ReplaceAllString(raw, "\n")
		raw = tagRe.ReplaceAllString(raw, " ")
		raw = unescapeHTML(raw)
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spaceRe.ReplaceAllString(line, " "))
		if line != "" {
			out = append(out, line)
		}
	}
	text := strings.Join(out, "\n")
	return strings.TrimSpace(blankLineRe.ReplaceAllString(text, "\n\n"))
}

func pageTitle(body []byte, fallback string) string {
	if match := titleRe.FindSubmatch(body); len(match) == 2 {
		title := strings.TrimSpace(unescapeHTML(string(match[1])))
		if title != "" {
			return title
		}
	}
	first := fallback
	if idx := strings.IndexByte(fallback, '\n'); idx > 0 {
		first = fallback[:idx]
	}
	first = strings.TrimSpace(first)
	runes := []rune(first)
	if len(runes) > 80 {
		first = string(runes[:80])
	}
	return first
}

func unescapeHTML(value string) string {
	replacer := strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", "\"", "&#39;", "'", "&apos;", "'", "&mdash;", "—",
	)
	return replacer.Replace(value)
}

// Slugify builds a safe file name from a page title or URL.
func Slugify(value string, fallback string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			builder.WriteRune('-')
		}
	}
	out := strings.Trim(builder.String(), "-.")
	runes := []rune(out)
	if len(runes) > 60 {
		out = string(runes[:60])
	}
	if out == "" {
		out = fallback
	}
	return out
}
