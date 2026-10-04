package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/url"
	"time"
)

var ErrUnauthenticated = errors.New("vault session unavailable")

type Client struct {
	origin, token string
	http          *http.Client
}

func NewClient(origin, token string) (*Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") || len(token) < 32 {
		return nil, errors.New("configure internal vault origin and service token")
	}
	return &Client{origin: origin, token: token, http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path, session, trace string, body any) (*http.Response, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, c.origin+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("X-JanSetu-Session", session)
	r.Header.Set("X-Request-ID", trace)
	r.Header.Set("Content-Type", "application/json")
	return c.http.Do(r)
}
func (c *Client) Ready(ctx context.Context) error {
	r, err := c.request(ctx, "GET", "/health/ready", "", "", nil)
	if err != nil {
		return errors.New("vault unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != 204 {
		return errors.New("vault unavailable")
	}
	return nil
}
func (c *Client) Aliases(ctx context.Context, session, trace string, submission uuid.UUID) (Grant, error) {
	method := "GET"
	var body any
	if submission != uuid.Nil {
		method = "POST"
		body = map[string]any{"submissionId": submission}
	}
	r, err := c.request(ctx, method, "/aliases", session, trace, body)
	if err != nil {
		return Grant{}, errors.New("vault unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode == 401 {
		return Grant{}, ErrUnauthenticated
	}
	if r.StatusCode != 200 {
		return Grant{}, errors.New("vault unavailable")
	}
	var grant Grant
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if d.Decode(&grant) != nil || len(grant.Signature) != 64 || grant.Claim == "" {
		return Grant{}, errors.New("vault response unavailable")
	}
	return grant, nil
}
