package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/cli/cli/internal/ghinstance"
	"github.com/henvic/httpretty"
	"github.com/shurcooL/graphql"
)

// ClientOption represents an argument to NewClient
type ClientOption = func(http.RoundTripper) http.RoundTripper

// NewHTTPClient initializes an http.Client
func NewHTTPClient(opts ...ClientOption) *http.Client {
	tr := http.DefaultTransport
	for _, opt := range opts {
		tr = opt(tr)
	}
	return &http.Client{Transport: tr}
}

// NewClient initializes a Client
func NewClient(opts ...ClientOption) *Client {
	client := &Client{http: NewHTTPClient(opts...)}
	return client
}

// NewClientFromHTTP takes in an http.Client instance
func NewClientFromHTTP(httpClient *http.Client) *Client {
	client := &Client{http: httpClient}
	return client
}

// AddHeader turns a RoundTripper into one that adds a request header
func AddHeader(name, value string) ClientOption {
	return func(tr http.RoundTripper) http.RoundTripper {
		return &funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.Header.Get(name) == "" {
				req.Header.Add(name, value)
			}
			return tr.RoundTrip(req)
		}}
	}
}

// AddHeaderFunc is an AddHeader that gets the string value from a function
func AddHeaderFunc(name string, getValue func(*http.Request) (string, error)) ClientOption {
	return func(tr http.RoundTripper) http.RoundTripper {
		return &funcTripper{roundTrip: func(req *http.Request) (*http.Response, error) {
			if req.Header.Get(name) != "" {
				return tr.RoundTrip(req)
			}
			value, err := getValue(req)
			if err != nil {
				return nil, err
			}
			if value != "" {
				req.Header.Add(name, value)
			}
			return tr.RoundTrip(req)
		}}
	}
}

// VerboseLog enables request/response logging within a RoundTripper
func VerboseLog(out io.Writer, logTraffic bool, colorize bool) ClientOption {
	logger := &httpretty.Logger{
		Time:            true,
		TLS:             false,
		Colors:          colorize,
		RequestHeader:   logTraffic,
		RequestBody:     logTraffic,
		ResponseHeader:  logTraffic,
		ResponseBody:    logTraffic,
		Formatters:      []httpretty.Formatter{&httpretty.JSONFormatter{}},
		MaxResponseBody: 10000,
	}
	logger.SetOutput(out)
	logger.SetBodyFilter(func(h http.Header) (skip bool, err error) {
		return !inspectableMIMEType(h.Get("Content-Type")), nil
	})
	return logger.RoundTripper
}

// ReplaceTripper substitutes the underlying RoundTripper with a custom one
func ReplaceTripper(tr http.RoundTripper) ClientOption {
	return func(http.RoundTripper) http.RoundTripper {
		return tr
	}
}

type funcTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (tr funcTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return tr.roundTrip(req)
}

// Client facilitates making HTTP requests to the GitHub API
type Client struct {
	http *http.Client
}

type graphQLResponse struct {
	Data   interface{}
	Errors []GraphQLError
}

// GraphQLError is a single error returned in a GraphQL response
type GraphQLError struct {
	Type    string
	Path    []string
	Message string
}

// GraphQLErrorResponse contains errors returned in a GraphQL response
type GraphQLErrorResponse struct {
	Errors []GraphQLError
}

func (gr GraphQLErrorResponse) Error() string {
	errorMessages := make([]string, 0, len(gr.Errors))
	for _, e := range gr.Errors {
		errorMessages = append(errorMessages, e.Message)
	}
	return fmt.Sprintf("GraphQL error: %s", strings.Join(errorMessages, "\n"))
}

// HTTPError is an error returned by a failed API call
type HTTPError struct {
	StatusCode  int
	RequestURL  *url.URL
	Message     string
	OAuthScopes string
}

func (err HTTPError) Error() string {
	if msgs := strings.SplitN(err.Message, "\n", 2); len(msgs) > 1 {
		return fmt.Sprintf("HTTP %d: %s (%s)\n%s", err.StatusCode, msgs[0], err.RequestURL, msgs[1])
	} else if err.Message != "" {
		return fmt.Sprintf("HTTP %d: %s (%s)", err.StatusCode, err.Message, err.RequestURL)
	}
	return fmt.Sprintf("HTTP %d (%s)", err.StatusCode, err.RequestURL)
}

type MissingScopesError struct {
	MissingScopes []string
}

func (e MissingScopesError) Error() string {
	var missing []string
	for _, s := range e.MissingScopes {
		missing = append(missing, fmt.Sprintf("'%s'", s))
	}
	scopes := strings.Join(missing, ", ")

	if len(e.MissingScopes) == 1 {
		return "missing required scope " + scopes
	}
	return "missing required scopes " + scopes
}

func (c Client) HasMinimumScopes(hostname string) error {
	apiEndpoint := ghinstance.RESTPrefix(hostname)

	req, err := http.NewRequest("GET", apiEndpoint, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}

	defer func() {
		// Ensure the response body is fully read and closed
		// before we reconnect, so that we reuse the same TCPconnection.
		_, _ = io.Copy(ioutil.Discard, res.Body)
		res.Body.Close()
	}()

	if res.StatusCode != 200 {
		return HandleHTTPError(res)
	}

	scopesHeader := res.Header.Get("X-Oauth-Scopes")
	if scopesHeader == "" {
		// if the token reports no scopes, assume that it's an integration token and give up on
		// detecting its capabilities
		return nil
	}

	search := map[string]bool{
		"repo":      false,
		"read:org":  false,
		"admin:org": false,
	}
	for _, s := range strings.Split(scopesHeader, ",") {
		search[strings.TrimSpace(s)] = true
	}

	var missingScopes []string
	if !search["repo"] {
		missingScopes = append(missingScopes, "repo")
	}

	if !search["read:org"] && !search["admin:org"] {
		missingScopes = append(missingScopes, "read:org")
	}

	if len(missingScopes) > 0 {
		return &MissingScopesError{MissingScopes: missingScopes}
	}
	return nil
}

// GraphQL performs a GraphQL request and parses the response
func (c Client) GraphQL(hostname string, query string, variables map[string]interface{}, data interface{}) error {
	reqBody, err := json.Marshal(map[string]interface{}{"query": query, "variables": variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", ghinstance.GraphQLEndpoint(hostname), bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return handleResponse(resp, data)
}

func graphQLClient(h *http.Client, hostname string) *graphql.Client {
	return graphql.NewClient(ghinstance.GraphQLEndpoint(hostname), h)
}

// REST performs a REST request and parses the response.
func (c Client) REST(hostname string, method string, p string, body io.Reader, data interface{}) error {
	url := ghinstance.RESTPrefix(hostname) + p
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !success {
		return HandleHTTPError(resp)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, &data)
	if err != nil {
		return err
	}

	return nil
}

func handleResponse(resp *http.Response, data interface{}) error {
	success := resp.StatusCode >= 200 && resp.StatusCode < 300

	if !success {
		return HandleHTTPError(resp)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	gr := &graphQLResponse{Data: data}
	err = json.Unmarshal(body, &gr)
	if err != nil {
		return err
	}

	if len(gr.Errors) > 0 {
		return &GraphQLErrorResponse{Errors: gr.Errors}
	}
	return nil
}

func HandleHTTPError(resp *http.Response) error {
	httpError := HTTPError{
		StatusCode:  resp.StatusCode,
		RequestURL:  resp.Request.URL,
		OAuthScopes: resp.Header.Get("X-Oauth-Scopes"),
	}

	if !jsonTypeRE.MatchString(resp.Header.Get("Content-Type")) {
		httpError.Message = resp.Status
		return httpError
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		httpError.Message = err.Error()
		return httpError
	}

	var parsedBody struct {
		Message string `json:"message"`
		Errors  []json.RawMessage
	}
	if err := json.Unmarshal(body, &parsedBody); err != nil {
		return httpError
	}

	type errorObject struct {
		Message  string
		Resource string
		Field    string
		Code     string
	}

	messages := []string{parsedBody.Message}
	for _, raw := range parsedBody.Errors {
		switch raw[0] {
		case '"':
			var errString string
			_ = json.Unmarshal(raw, &errString)
			messages = append(messages, errString)
		case '{':
			var errInfo errorObject
			_ = json.Unmarshal(raw, &errInfo)
			msg := errInfo.Message
			if errInfo.Code != "custom" {
				msg = fmt.Sprintf("%s.%s %s", errInfo.Resource, errInfo.Field, errorCodeToMessage(errInfo.Code))
			}
			if msg != "" {
				messages = append(messages, msg)
			}
		}
	}
	httpError.Message = strings.Join(messages, "\n")

	return httpError
}

func errorCodeToMessage(code string) string {
	// https://docs.github.com/en/rest/overview/resources-in-the-rest-api#client-errors
	switch code {
	case "missing", "missing_field":
		return "is missing"
	case "invalid", "unprocessable":
		return "is invalid"
	case "already_exists":
		return "already exists"
	default:
		return code
	}
}

var jsonTypeRE = regexp.MustCompile(`[/+]json($|;)`)

func inspectableMIMEType(t string) bool {
	return strings.HasPrefix(t, "text/") || jsonTypeRE.MatchString(t)
}
