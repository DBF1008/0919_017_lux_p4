package request

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	cookiemonster "github.com/MercuryEngineering/CookieMonster"
	"github.com/fatih/color"
	"github.com/kr/pretty"
	"github.com/pkg/errors"

	"github.com/iawia002/lux/config"
	"github.com/iawia002/lux/logging"
	"github.com/iawia002/lux/metrics"
)

const requestIDHeader = "X-Request-ID"

var (
	retryTimes int
	rawCookie  string
	userAgent  string
	refer      string
	debug      bool
)

// Options defines common request options.
type Options struct {
	RetryTimes int
	Cookie     string
	UserAgent  string
	Refer      string
	Debug      bool
	Silent     bool
}

// SetOptions sets the common request option.
func SetOptions(opt Options) {
	retryTimes = opt.RetryTimes
	rawCookie = opt.Cookie
	userAgent = opt.UserAgent
	refer = opt.Refer
	debug = opt.Debug
}

// Request base request
func Request(method, url string, body io.Reader, headers map[string]string) (*http.Response, error) {
	return RequestContext(context.Background(), method, url, body, headers)
}

// RequestContext is like Request but carries a context used for cancellation
// and request_id propagation. A unique request id is attached to every call
// (inherited from ctx when present, otherwise freshly generated) and exposed
// both in structured logs and via the X-Request-ID request header.
func RequestContext(ctx context.Context, method, requestURL string, body io.Reader, headers map[string]string) (*http.Response, error) {
	requestID := logging.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = logging.NewRequestID()
		ctx = logging.WithRequestID(ctx, requestID)
	}
	logger := logging.FromContext(ctx)

	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DisableCompression:  true,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Minute,
		Jar:       jar,
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	for k, v := range config.FakeHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if _, ok := headers["Referer"]; !ok {
		req.Header.Set("Referer", requestURL)
	}
	if rawCookie != "" {
		// parse cookies in Netscape HTTP cookie format
		cookies, _ := cookiemonster.ParseString(rawCookie)
		if len(cookies) > 0 {
			for _, c := range cookies {
				req.AddCookie(c)
			}
		} else {
			// cookie is not Netscape HTTP format, set it directly
			// a=b; c=d
			req.Header.Set("Cookie", rawCookie)
		}
	}

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	if refer != "" {
		req.Header.Set("Referer", refer)
	}
	req.Header.Set(requestIDHeader, requestID)

	host := hostOf(requestURL)
	var (
		res          *http.Response
		requestError error
	)
	startedAt := time.Now()
	for i := 0; ; i++ {
		res, requestError = client.Do(req)
		if requestError == nil && res.StatusCode < 400 {
			break
		}
		if i+1 >= retryTimes {
			if requestError != nil {
				metrics.IncHTTPRequest(method, host, 0)
				logger.Error(
					"http request failed",
					"method", method,
					"url", requestURL,
					"attempts", i+1,
					"error", requestError,
				)
				return nil, errors.WithStack(errors.Errorf("request error: %v", requestError))
			}
			metrics.IncHTTPRequest(method, host, res.StatusCode)
			logger.Error(
				"http request failed",
				"method", method,
				"url", requestURL,
				"attempts", i+1,
				"status_code", res.StatusCode,
			)
			return nil, errors.WithStack(errors.Errorf("%s request error: HTTP %d", requestURL, res.StatusCode))
		}
		metrics.IncHTTPRetry()
		if requestError != nil {
			logger.Warn(
				"http request retry",
				"method", method,
				"url", requestURL,
				"attempt", i+1,
				"error", requestError,
			)
		} else {
			logger.Warn(
				"http request retry",
				"method", method,
				"url", requestURL,
				"attempt", i+1,
				"status_code", res.StatusCode,
			)
		}
		select {
		case <-ctx.Done():
			metrics.IncHTTPRequest(method, host, 0)
			return nil, errors.WithStack(ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
	metrics.IncHTTPRequest(method, host, res.StatusCode)
	logger.Debug(
		"http request completed",
		"method", method,
		"url", requestURL,
		"status_code", res.StatusCode,
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
	if debug {
		blue := color.New(color.FgBlue)
		fmt.Fprintln(color.Output)
		blue.Fprint(color.Output, "URL:         ")
		fmt.Fprintf(color.Output, "%s\n", requestURL)
		blue.Fprint(color.Output, "Method:      ")
		fmt.Fprintf(color.Output, "%s\n", method)
		blue.Fprint(color.Output, "Request-ID:  ")
		fmt.Fprintf(color.Output, "%s\n", requestID)
		blue.Fprint(color.Output, "Headers:     ")
		pretty.Fprintf(color.Output, "%# v\n", req.Header) // nolint
		blue.Fprint(color.Output, "Status Code: ")
		if res.StatusCode >= 400 {
			fmt.Fprintf(color.Output, "%s\n", color.RedString("%d", res.StatusCode))
		} else {
			fmt.Fprintf(color.Output, "%s\n", color.GreenString("%d", res.StatusCode))
		}
	}
	return res, nil
}

func hostOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	return parsed.Host
}

// Get get request
func Get(url, refer string, headers map[string]string) (string, error) {
	body, err := GetByte(url, refer, headers)
	return string(body), err
}

// GetByte get request
func GetByte(url, refer string, headers map[string]string) ([]byte, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	if refer != "" {
		headers["Referer"] = refer
	}
	res, err := Request(http.MethodGet, url, nil, headers)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer res.Body.Close() // nolint

	var reader io.ReadCloser
	switch res.Header.Get("Content-Encoding") {
	case "gzip":
		reader, _ = gzip.NewReader(res.Body)
	case "deflate":
		reader = flate.NewReader(res.Body)
	default:
		reader = res.Body
	}
	defer reader.Close() // nolint

	body, err := io.ReadAll(reader)
	if err != nil && err != io.EOF {
		return nil, errors.WithStack(err)
	}
	return body, nil
}

// Headers return the HTTP Headers of the url
func Headers(url, refer string) (http.Header, error) {
	headers := map[string]string{
		"Referer": refer,
	}
	res, err := Request(http.MethodGet, url, nil, headers)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	defer res.Body.Close() // nolint
	return res.Header, nil
}

// Size get size of the url
func Size(url, refer string) (int64, error) {
	h, err := Headers(url, refer)
	if err != nil {
		return 0, err
	}
	s := h.Get("Content-Length")
	if s == "" {
		return 0, errors.New("Content-Length is not present")
	}
	size, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return size, nil
}

// ContentType get Content-Type of the url
func ContentType(url, refer string) (string, error) {
	h, err := Headers(url, refer)
	if err != nil {
		return "", err
	}
	s := h.Get("Content-Type")
	// handle Content-Type like this: "text/html; charset=utf-8"
	return strings.Split(s, ";")[0], nil
}
