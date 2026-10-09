// Package submission only addresses fixed framework HTTP operations. It never
// imports a lower store or execution component and never calls trading writes.
package submission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type HTTPOptions struct {
	BaseURL, Token             string
	Binding                    d.Binding
	ResearchOnly               bool
	Client                     *http.Client
	MaxResponseBytes, MaxPages int
}
type HTTPClient struct{ options HTTPOptions }

func NewHTTP(options HTTPOptions) (*HTTPClient, error) {
	u, e := url.Parse(options.BaseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!d.Has([]string{"http", "https"}, u.Scheme) || strings.TrimRight(u.Path, "/") != "" ||
		!options.Binding.Valid() || options.Token == "" || options.Client == nil || options.Client.Timeout <= 0 ||
		options.MaxResponseBytes <= 0 || options.MaxPages <= 0 {
		return nil, d.Fail("FRAMEWORK_CLIENT_CONFIGURATION_REQUIRED", 503)
	}
	client := *options.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options.Client = &client
	options.BaseURL = strings.TrimRight(options.BaseURL, "/")
	return &HTTPClient{options}, nil
}
func (c *HTTPClient) Binding() d.Binding { return c.options.Binding }
func (c *HTTPClient) ResearchOnly() bool { return c.options.ResearchOnly }
func (c *HTTPClient) call(ctx context.Context, method, path string, query url.Values, body any, target any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return d.Fail("FRAMEWORK_REQUEST_INVALID", 422)
		}
	}
	endpoint := c.options.BaseURL + "/api/v2/strategy" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return d.Fail("FRAMEWORK_REQUEST_INVALID", 422)
	}
	request.Header.Set("Authorization", "Bearer "+c.options.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.options.Client.Do(request)
	if err != nil {
		return d.Fail("FRAMEWORK_DELIVERY_UNKNOWN", 503)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(c.options.MaxResponseBytes)+1))
	if err != nil || len(data) > c.options.MaxResponseBytes {
		return d.Fail("FRAMEWORK_RESPONSE_UNAVAILABLE", 503)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code := "FRAMEWORK_REQUEST_REJECTED"
		switch response.StatusCode {
		case 401:
			code = "FRAMEWORK_AUTHENTICATION_REQUIRED"
		case 403:
			code = "FRAMEWORK_SCOPE_FORBIDDEN"
		case 409:
			code = "FRAMEWORK_CONFLICT"
			// Only the existing closed P2 envelope can identify a retryable CAS
			// conflict. Other conflicts must not acquire retry eligibility.
			var problem struct {
				Code          string  `json:"code"`
				Message       string  `json:"message"`
				Field         *string `json:"field"`
				CorrelationID *string `json:"correlation_id"`
				Retryable     bool    `json:"retryable"`
			}
			if d.DecodePrivate(data, &problem) == nil && problem.Code == "AGGREGATE_VERSION_CONFLICT" && problem.Message == problem.Code {
				code = "FRAMEWORK_AGGREGATE_VERSION_CONFLICT"
			}
		case 422:
			code = "FRAMEWORK_REQUEST_INVALID"
		}
		return d.Fail(code, response.StatusCode)
	}
	if target == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return nil
}
func (c *HTTPClient) check(ctx context.Context) error {
	var health struct {
		Environment, Listener string
		SchemaVersion         string `json:"schema_version"`
	}
	if err := c.call(ctx, "GET", "/health", nil, nil, &health); err != nil {
		return err
	}
	listener := "WORKLOAD"
	if c.ResearchOnly() {
		listener = "PUBLIC_RESEARCH"
	}
	if health.Environment != c.Binding().Environment || health.Listener != listener || health.SchemaVersion != "strategy-2.0" {
		return d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
	}
	return nil
}
func (c *HTTPClient) Version(ctx context.Context, object string) (int, error) {
	if object != "" && !d.ValidID(object) {
		return 0, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return 0, err
	}
	var page struct {
		SnapshotVersion int               `json:"snapshot_version"`
		Items           []json.RawMessage `json:"items"`
	}
	q := url.Values{}
	if object != "" {
		q.Set("object_id", object)
	}
	if err := c.call(ctx, "GET", "/objects", q, nil, &page); err != nil {
		return 0, err
	}
	if page.SnapshotVersion < 0 || page.Items == nil {
		return 0, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return page.SnapshotVersion, nil
}
func (c *HTTPClient) CreateObject(ctx context.Context, request dto.CreateObject) (dto.ObservedObject, error) {
	var object dto.ObservedObject
	if request.Object.InstanceID != c.Binding().InstanceID || request.Object.Environment != c.Binding().Environment ||
		request.Object.TradingRunKey.Environment != c.Binding().Environment || !d.ValidID(request.Object.ObjectID) {
		return object, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
	}
	if err := c.check(ctx); err != nil {
		return object, err
	}
	if err := c.call(ctx, "POST", "/objects", nil, request, &object); err != nil {
		return object, err
	}
	if object.ObjectID != request.Object.ObjectID || object.InstanceID != c.Binding().InstanceID || object.Environment != c.Binding().Environment {
		return object, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return object, nil
}
func (c *HTTPClient) Object(ctx context.Context, id string) (*dto.ObservedObject, error) {
	if !d.ValidID(id) {
		return nil, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	// The legacy missing-object endpoint records a rejected-request audit. Use
	// the side-effect-free directory first so recovery does not mutate a registry
	// merely to discover that the object has not been created.
	cursor := ""
	seen := map[string]bool{}
	found := false
	for pageIndex := 0; pageIndex < c.options.MaxPages; pageIndex++ {
		var page struct {
			Items []struct {
				ObjectID string `json:"object_id"`
			} `json:"items"`
			Cursor *string `json:"cursor"`
		}
		query := url.Values{}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if err := c.call(ctx, "GET", "/objects", query, nil, &page); err != nil {
			return nil, err
		}
		if page.Items == nil {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		for _, item := range page.Items {
			if item.ObjectID == id {
				found = true
				break
			}
		}
		if found {
			break
		}
		if page.Cursor == nil {
			return nil, nil
		}
		if *page.Cursor == "" || seen[*page.Cursor] {
			return nil, d.Fail("FRAMEWORK_QUERY_CURSOR_INVALID", 503)
		}
		cursor = *page.Cursor
		seen[cursor] = true
	}
	if !found {
		return nil, d.Fail("FRAMEWORK_QUERY_BUDGET_EXCEEDED", 503)
	}
	var object dto.ObservedObject
	err := c.call(ctx, "GET", "/objects/"+id, nil, nil, &object)
	if err != nil {
		var known *d.Error
		if errors.As(err, &known) && known.Status == 404 {
			return nil, nil
		}
		return nil, err
	}
	if object.ObjectID != id || object.InstanceID != c.Binding().InstanceID || object.Environment != c.Binding().Environment {
		return nil, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
	}
	return &object, nil
}
func (c *HTTPClient) RegisterEvent(ctx context.Context, request dto.EventCommand, revision bool) (dto.Event, error) {
	var event dto.Event
	if !d.ValidID(request.Event.EventID) {
		return event, d.Fail("FRAMEWORK_EVENT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return event, err
	}
	path := "/events"
	if revision {
		path += "/" + request.Event.EventID + "/revisions"
	}
	if err := c.call(ctx, "POST", path, nil, request, &event); err != nil {
		return event, err
	}
	if event.EventID != request.Event.EventID || event.FactVersion != request.Event.FactVersion {
		return event, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return event, nil
}
func (c *HTTPClient) Submit(ctx context.Context, request dto.ScoreCommand) (dto.AdmissionReceipt, error) {
	var receipt dto.AdmissionReceipt
	s := request.Score
	if !d.ValidID(s.EventID) || !d.ValidID(s.ObjectID) || !d.ValidID(s.SubmissionID) || s.PreviousScoreID != nil && !d.ValidID(*s.PreviousScoreID) {
		return receipt, d.Fail("FRAMEWORK_SCORE_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return receipt, err
	}
	path := "/events/" + s.EventID + "/scores"
	if s.RevisionKind == "REVISION" {
		if s.PreviousScoreID == nil {
			return receipt, d.Fail("FRAMEWORK_REVISION_REQUIRED", 422)
		}
		path += "/" + *s.PreviousScoreID + "/revisions"
	}
	if err := c.call(ctx, "POST", path, nil, request, &receipt); err != nil {
		return receipt, err
	}
	if receipt.SubmissionID != s.SubmissionID || !receiptValid(receipt) {
		return receipt, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	if c.ResearchOnly() && !d.Has([]string{"RESEARCH_ONLY", "DRAFT"}, receipt.State) {
		return receipt, d.Fail("FRAMEWORK_RESEARCH_SCOPE_VIOLATION", 403)
	}
	return receipt, nil
}
func (c *HTTPClient) EventExists(ctx context.Context, event, object string, revision int) (bool, error) {
	if !d.ValidID(event) || !d.ValidID(object) || revision < 1 {
		return false, d.Fail("FRAMEWORK_EVENT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return false, err
	}
	var page struct {
		Items []struct {
			EventID     string   `json:"event_id"`
			FactVersion int      `json:"fact_version"`
			ObjectIDs   []string `json:"object_ids"`
		} `json:"items"`
	}
	err := c.call(ctx, "GET", "/events/"+event, url.Values{"object_id": {object}, "revision": {strconv.Itoa(revision)}}, nil, &page)
	if err != nil {
		var known *d.Error
		if errors.As(err, &known) && known.Status == 404 {
			return false, nil
		}
		return false, err
	}
	if len(page.Items) != 1 {
		return false, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	row := page.Items[0]
	if row.EventID != event || row.FactVersion != revision || !d.Has(row.ObjectIDs, object) {
		return false, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
	}
	return true, nil
}
func receiptValid(r dto.AdmissionReceipt) bool {
	return d.ValidID(r.SubmissionID) && r.CurrentVersion >= 0 && d.Has([]string{"DRAFT", "RESEARCH_ONLY", "QUARANTINED", "READY_PENDING_PRICE", "APPLIED", "SUPERSEDED"}, r.State)
}
func (c *HTTPClient) Receipt(ctx context.Context, event, object, id string) (*dto.AdmissionReceipt, error) {
	if !d.ValidID(event) || !d.ValidID(object) || !d.ValidID(id) {
		return nil, d.Fail("FRAMEWORK_SCORE_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	q := url.Values{"object_id": {object}}
	seen := map[string]bool{}
	for count := 0; count < c.options.MaxPages; count++ {
		var page struct {
			Items []struct {
				ID       string                `json:"submission_id"`
				ObjectID string                `json:"object_id"`
				EventID  string                `json:"event_id"`
				Receipt  *dto.AdmissionReceipt `json:"admission_receipt"`
			} `json:"items"`
			Cursor *string `json:"cursor"`
		}
		err := c.call(ctx, "GET", "/events/"+event+"/scores", q, nil, &page)
		if err != nil {
			var known *d.Error
			if errors.As(err, &known) && known.Status == 404 {
				return nil, nil
			}
			return nil, err
		}
		for _, row := range page.Items {
			if row.ObjectID != object || row.EventID != event {
				return nil, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
			}
			if row.ID == id {
				if row.Receipt == nil || row.Receipt.SubmissionID != id || !receiptValid(*row.Receipt) {
					return nil, d.Fail("FRAMEWORK_RECEIPT_NOT_RECORDED", 503)
				}
				return row.Receipt, nil
			}
		}
		if page.Cursor == nil {
			return nil, nil
		}
		if *page.Cursor == "" || seen[*page.Cursor] {
			return nil, d.Fail("FRAMEWORK_CURSOR_INVALID", 409)
		}
		seen[*page.Cursor] = true
		q.Set("cursor", *page.Cursor)
	}
	return nil, d.Fail("FRAMEWORK_QUERY_BUDGET_EXCEEDED", 503)
}
