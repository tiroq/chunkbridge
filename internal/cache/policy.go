package cache

import (
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// staticExtensions are file suffixes that receive a default TTL when the
// response carries no explicit Cache-Control or Expires header.
var staticExtensions = map[string]bool{
	".css":  true,
	".js":   true,
	".mjs":  true,
	".map":  true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".webp": true,
	".svg":  true,
	".ico":  true,
	".json": true,
}

// cacheableStatuses are HTTP status codes that may be cached according to
// RFC 7231 §6.1 and common conservative practice.
var cacheableStatuses = map[int]bool{
	200: true,
	203: true,
	204: true,
	206: true,
	301: true,
	308: true,
}

// ─── request cacheability ─────────────────────────────────────────────────────

// RequestOptions controls which request attributes bypass the cache.
type RequestOptions struct {
	CacheWithAuthorization bool
	CacheWithCookies       bool
}

// IsRequestCacheable reports whether r may use or populate the cache.
// Returns false for non-GET/HEAD methods, requests with Authorization or Cookie
// headers (unless opted in), and requests with Cache-Control: no-store or
// no-cache (revalidation is not implemented).
func IsRequestCacheable(r *http.Request, opts RequestOptions) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !opts.CacheWithAuthorization && r.Header.Get("Authorization") != "" {
		return false
	}
	if !opts.CacheWithCookies && r.Header.Get("Cookie") != "" {
		return false
	}
	cc := r.Header.Get("Cache-Control")
	if hasDirective(cc, "no-store") {
		return false
	}
	// Treat no-cache as a bypass; revalidation (If-None-Match, etc.) is not
	// implemented in this release.
	if hasDirective(cc, "no-cache") {
		return false
	}
	return true
}

// ─── response cacheability ────────────────────────────────────────────────────

// ResponseOptions controls which response attributes prevent caching.
type ResponseOptions struct {
	MaxEntryBytes int64
	CachePrivate  bool
}

// IsResponseCacheable reports whether a response with the given attributes may
// be stored in the cache.
//
// Vary support: only "Accept-Encoding" is supported. Any other Vary field
// causes the response to be skipped. This is a known limitation documented in
// docs/architecture.md.
func IsResponseCacheable(method string, statusCode int, respHeader http.Header, bodySize int64, opts ResponseOptions) bool {
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	if !cacheableStatuses[statusCode] {
		return false
	}
	cc := respHeader.Get("Cache-Control")
	if hasDirective(cc, "no-store") {
		return false
	}
	if !opts.CachePrivate && hasDirective(cc, "private") {
		return false
	}
	if !opts.CachePrivate && respHeader.Get("Set-Cookie") != "" {
		return false
	}
	vary := respHeader.Get("Vary")
	if vary == "*" {
		return false
	}
	// Only Accept-Encoding is supported as a Vary field.
	if vary != "" {
		for _, v := range splitComma(vary) {
			v = strings.TrimSpace(strings.ToLower(v))
			if v != "accept-encoding" {
				return false
			}
		}
	}
	if opts.MaxEntryBytes > 0 && bodySize > opts.MaxEntryBytes {
		return false
	}
	return true
}

// ─── TTL ──────────────────────────────────────────────────────────────────────

// TTLFor computes the TTL for a response from the Cache-Control max-age
// directive, the Expires header, or a heuristic default for static asset
// extensions. Returns 0 if no positive TTL can be determined, meaning the
// response should not be cached on freshness grounds.
func TTLFor(respHeader http.Header, reqURL string, defaultTTLSec int, now time.Time) time.Duration {
	cc := respHeader.Get("Cache-Control")
	if cc != "" {
		for _, directive := range splitComma(cc) {
			directive = strings.TrimSpace(directive)
			if strings.HasPrefix(strings.ToLower(directive), "max-age=") {
				val := directive[len("max-age="):]
				if n, err := strconv.Atoi(val); err == nil && n >= 0 {
					return time.Duration(n) * time.Second
				}
			}
		}
	}
	if exp := respHeader.Get("Expires"); exp != "" {
		if t, err := http.ParseTime(exp); err == nil && t.After(now) {
			return t.Sub(now)
		}
	}
	// Heuristic: apply default TTL to static-looking URL paths.
	if defaultTTLSec > 0 {
		rawPath := reqURL
		// Strip query string before extracting extension.
		if idx := strings.IndexByte(rawPath, '?'); idx >= 0 {
			rawPath = rawPath[:idx]
		}
		ext := strings.ToLower(path.Ext(rawPath))
		if staticExtensions[ext] {
			return time.Duration(defaultTTLSec) * time.Second
		}
	}
	return 0
}

// ─── cache key ────────────────────────────────────────────────────────────────

// BuildKey returns the cache key for method, rawURL, and the request's
// Accept-Encoding header. Accept-Encoding is always included so that responses
// with Vary: Accept-Encoding are stored and retrieved correctly.
//
// Other Vary fields are not supported; responses with unsupported Vary are
// rejected by IsResponseCacheable.
func BuildKey(method, rawURL string, reqHeader http.Header) string {
	ae := ""
	if reqHeader != nil {
		ae = reqHeader.Get("Accept-Encoding")
	}
	return method + "\x00" + rawURL + "\x00" + ae
}

// ─── header copy ──────────────────────────────────────────────────────────────

// CopyHeader returns a deep copy of src so that mutations of the original or
// the copy do not affect each other.
func CopyHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for k, vs := range src {
		cp := make([]string, len(vs))
		copy(cp, vs)
		dst[k] = cp
	}
	return dst
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// hasDirective reports whether the Cache-Control header value contains
// directive (case-insensitive). Values after '=' are ignored.
func hasDirective(cc, directive string) bool {
	for _, d := range splitComma(cc) {
		d = strings.TrimSpace(d)
		if idx := strings.IndexByte(d, '='); idx >= 0 {
			d = d[:idx]
		}
		if strings.EqualFold(d, directive) {
			return true
		}
	}
	return false
}

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
