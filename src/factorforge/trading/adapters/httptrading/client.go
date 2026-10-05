package httptrading

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	HTTP            *http.Client
	Endpoint, Token string
}

func (c *Client) Call(ctx context.Context, method, path string, query map[string][]string, body any) (json.RawMessage, error) {
	fail := func(status int) (json.RawMessage, error) {
		return nil, &d.Error{Code: "TRADING_API_REQUEST_FAILED", Status: status}
	}
	if c.HTTP == nil || c.Endpoint == "" || c.Token == "" || !strings.HasPrefix(path, "/api/v2/trading/") {
		return fail(503)
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return fail(422)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Endpoint, "/")+path+"?"+url.Values(query).Encode(), bytes.NewReader(data))
	if err != nil {
		return fail(503)
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Content-Type", "application/json")
	safe := *c.HTTP
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safe.Do(request)
	if err != nil {
		return fail(503)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if err != nil || len(result) > 16<<20 || !json.Valid(result) {
		return fail(503)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fail(response.StatusCode)
	}
	return result, nil
}
