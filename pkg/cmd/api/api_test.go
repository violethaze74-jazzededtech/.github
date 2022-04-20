package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewCmdApi(t *testing.T) {
	f := &cmdutil.Factory{}

	tests := []struct {
		name     string
		cli      string
		wants    ApiOptions
		wantsErr bool
	}{
		{
			name: "no flags",
			cli:  "graphql",
			wants: ApiOptions{
				RequestMethod:       "GET",
				RequestMethodPassed: false,
				RequestPath:         "graphql",
				RequestInputFile:    "",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            false,
			},
			wantsErr: false,
		},
		{
			name: "override method",
			cli:  "repos/octocat/Spoon-Knife -XDELETE",
			wants: ApiOptions{
				RequestMethod:       "DELETE",
				RequestMethodPassed: true,
				RequestPath:         "repos/octocat/Spoon-Knife",
				RequestInputFile:    "",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            false,
			},
			wantsErr: false,
		},
		{
			name: "with fields",
			cli:  "graphql -f query=QUERY -F body=@file.txt",
			wants: ApiOptions{
				RequestMethod:       "GET",
				RequestMethodPassed: false,
				RequestPath:         "graphql",
				RequestInputFile:    "",
				RawFields:           []string{"query=QUERY"},
				MagicFields:         []string{"body=@file.txt"},
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            false,
			},
			wantsErr: false,
		},
		{
			name: "with headers",
			cli:  "user -H 'accept: text/plain' -i",
			wants: ApiOptions{
				RequestMethod:       "GET",
				RequestMethodPassed: false,
				RequestPath:         "user",
				RequestInputFile:    "",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string{"accept: text/plain"},
				ShowResponseHeaders: true,
				Paginate:            false,
			},
			wantsErr: false,
		},
		{
			name: "with pagination",
			cli:  "repos/OWNER/REPO/issues --paginate",
			wants: ApiOptions{
				RequestMethod:       "GET",
				RequestMethodPassed: false,
				RequestPath:         "repos/OWNER/REPO/issues",
				RequestInputFile:    "",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            true,
			},
			wantsErr: false,
		},
		{
			name:     "POST pagination",
			cli:      "-XPOST repos/OWNER/REPO/issues --paginate",
			wantsErr: true,
		},
		{
			name: "GraphQL pagination",
			cli:  "-XPOST graphql --paginate",
			wants: ApiOptions{
				RequestMethod:       "POST",
				RequestMethodPassed: true,
				RequestPath:         "graphql",
				RequestInputFile:    "",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            true,
			},
			wantsErr: false,
		},
		{
			name:     "input pagination",
			cli:      "--input repos/OWNER/REPO/issues --paginate",
			wantsErr: true,
		},
		{
			name: "with request body from file",
			cli:  "user --input myfile",
			wants: ApiOptions{
				RequestMethod:       "GET",
				RequestMethodPassed: false,
				RequestPath:         "user",
				RequestInputFile:    "myfile",
				RawFields:           []string(nil),
				MagicFields:         []string(nil),
				RequestHeaders:      []string(nil),
				ShowResponseHeaders: false,
				Paginate:            false,
			},
			wantsErr: false,
		},
		{
			name:     "no arguments",
			cli:      "",
			wantsErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCmdApi(f, func(o *ApiOptions) error {
				assert.Equal(t, tt.wants.RequestMethod, o.RequestMethod)
				assert.Equal(t, tt.wants.RequestMethodPassed, o.RequestMethodPassed)
				assert.Equal(t, tt.wants.RequestPath, o.RequestPath)
				assert.Equal(t, tt.wants.RequestInputFile, o.RequestInputFile)
				assert.Equal(t, tt.wants.RawFields, o.RawFields)
				assert.Equal(t, tt.wants.MagicFields, o.MagicFields)
				assert.Equal(t, tt.wants.RequestHeaders, o.RequestHeaders)
				assert.Equal(t, tt.wants.ShowResponseHeaders, o.ShowResponseHeaders)
				return nil
			})

			argv, err := shlex.Split(tt.cli)
			assert.NoError(t, err)
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			_, err = cmd.ExecuteC()
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func Test_apiRun(t *testing.T) {
	tests := []struct {
		name         string
		options      ApiOptions
		httpResponse *http.Response
		err          error
		stdout       string
		stderr       string
	}{
		{
			name: "success",
			httpResponse: &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`bam!`)),
			},
			err:    nil,
			stdout: `bam!`,
			stderr: ``,
		},
		{
			name: "show response headers",
			options: ApiOptions{
				ShowResponseHeaders: true,
			},
			httpResponse: &http.Response{
				Proto:      "HTTP/1.1",
				Status:     "200 Okey-dokey",
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`body`)),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			},
			err:    nil,
			stdout: "HTTP/1.1 200 Okey-dokey\nContent-Type: text/plain\r\n\r\nbody",
			stderr: ``,
		},
		{
			name: "success 204",
			httpResponse: &http.Response{
				StatusCode: 204,
				Body:       nil,
			},
			err:    nil,
			stdout: ``,
			stderr: ``,
		},
		{
			name: "REST error",
			httpResponse: &http.Response{
				StatusCode: 400,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`{"message": "THIS IS FINE"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutil.SilentError,
			stdout: `{"message": "THIS IS FINE"}`,
			stderr: "gh: THIS IS FINE (HTTP 400)\n",
		},
		{
			name: "GraphQL error",
			options: ApiOptions{
				RequestPath: "graphql",
			},
			httpResponse: &http.Response{
				StatusCode: 200,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`{"errors": [{"message":"AGAIN"}, {"message":"FINE"}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			},
			err:    cmdutil.SilentError,
			stdout: `{"errors": [{"message":"AGAIN"}, {"message":"FINE"}]}`,
			stderr: "gh: AGAIN\nFINE\n",
		},
		{
			name: "failure",
			httpResponse: &http.Response{
				StatusCode: 502,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`gateway timeout`)),
			},
			err:    cmdutil.SilentError,
			stdout: `gateway timeout`,
			stderr: "gh: HTTP 502\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, stdout, stderr := iostreams.Test()

			tt.options.IO = io
			tt.options.HttpClient = func() (*http.Client, error) {
				var tr roundTripper = func(req *http.Request) (*http.Response, error) {
					resp := tt.httpResponse
					resp.Request = req
					return resp, nil
				}
				return &http.Client{Transport: tr}, nil
			}

			err := apiRun(&tt.options)
			if err != tt.err {
				t.Errorf("expected error %v, got %v", tt.err, err)
			}

			if stdout.String() != tt.stdout {
				t.Errorf("expected output %q, got %q", tt.stdout, stdout.String())
			}
			if stderr.String() != tt.stderr {
				t.Errorf("expected error output %q, got %q", tt.stderr, stderr.String())
			}
		})
	}
}

func Test_apiRun_paginationREST(t *testing.T) {
	io, _, stdout, stderr := iostreams.Test()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewBufferString(`{"page":1}`)),
			Header: http.Header{
				"Link": []string{`<https://api.github.com/repositories/1227/issues?page=2>; rel="next", <https://api.github.com/repositories/1227/issues?page=3>; rel="last"`},
			},
		},
		{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewBufferString(`{"page":2}`)),
			Header: http.Header{
				"Link": []string{`<https://api.github.com/repositories/1227/issues?page=3>; rel="next", <https://api.github.com/repositories/1227/issues?page=3>; rel="last"`},
			},
		},
		{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewBufferString(`{"page":3}`)),
			Header:     http.Header{},
		},
	}

	options := ApiOptions{
		IO: io,
		HttpClient: func() (*http.Client, error) {
			var tr roundTripper = func(req *http.Request) (*http.Response, error) {
				resp := responses[requestCount]
				resp.Request = req
				requestCount++
				return resp, nil
			}
			return &http.Client{Transport: tr}, nil
		},

		RequestPath: "issues",
		Paginate:    true,
	}

	err := apiRun(&options)
	assert.NoError(t, err)

	assert.Equal(t, `{"page":1}{"page":2}{"page":3}`, stdout.String(), "stdout")
	assert.Equal(t, "", stderr.String(), "stderr")

	assert.Equal(t, "https://api.github.com/issues?per_page=100", responses[0].Request.URL.String())
	assert.Equal(t, "https://api.github.com/repositories/1227/issues?page=2", responses[1].Request.URL.String())
	assert.Equal(t, "https://api.github.com/repositories/1227/issues?page=3", responses[2].Request.URL.String())
}

func Test_apiRun_paginationGraphQL(t *testing.T) {
	io, _, stdout, stderr := iostreams.Test()

	requestCount := 0
	responses := []*http.Response{
		{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: ioutil.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": ["page one"],
					"pageInfo": {
						"endCursor": "PAGE1_END",
						"hasNextPage": true
					}
				}
			}`)),
		},
		{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{`application/json`}},
			Body: ioutil.NopCloser(bytes.NewBufferString(`{
				"data": {
					"nodes": ["page two"],
					"pageInfo": {
						"endCursor": "PAGE2_END",
						"hasNextPage": false
					}
				}
			}`)),
		},
	}

	options := ApiOptions{
		IO: io,
		HttpClient: func() (*http.Client, error) {
			var tr roundTripper = func(req *http.Request) (*http.Response, error) {
				resp := responses[requestCount]
				resp.Request = req
				requestCount++
				return resp, nil
			}
			return &http.Client{Transport: tr}, nil
		},

		RequestMethod: "POST",
		RequestPath:   "graphql",
		Paginate:      true,
	}

	err := apiRun(&options)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), `"page one"`)
	assert.Contains(t, stdout.String(), `"page two"`)
	assert.Equal(t, "", stderr.String(), "stderr")

	var requestData struct {
		Variables map[string]interface{}
	}

	bb, err := ioutil.ReadAll(responses[0].Request.Body)
	require.NoError(t, err)
	err = json.Unmarshal(bb, &requestData)
	require.NoError(t, err)
	_, hasCursor := requestData.Variables["endCursor"].(string)
	assert.Equal(t, false, hasCursor)

	bb, err = ioutil.ReadAll(responses[1].Request.Body)
	require.NoError(t, err)
	err = json.Unmarshal(bb, &requestData)
	require.NoError(t, err)
	endCursor, hasCursor := requestData.Variables["endCursor"].(string)
	assert.Equal(t, true, hasCursor)
	assert.Equal(t, "PAGE1_END", endCursor)
}

func Test_apiRun_inputFile(t *testing.T) {
	tests := []struct {
		name          string
		inputFile     string
		inputContents []byte

		contentLength    int64
		expectedContents []byte
	}{
		{
			name:          "stdin",
			inputFile:     "-",
			inputContents: []byte("I WORK OUT"),
			contentLength: 0,
		},
		{
			name:          "from file",
			inputFile:     "gh-test-file",
			inputContents: []byte("I WORK OUT"),
			contentLength: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, stdin, _, _ := iostreams.Test()
			resp := &http.Response{StatusCode: 204}

			inputFile := tt.inputFile
			if tt.inputFile == "-" {
				_, _ = stdin.Write(tt.inputContents)
			} else {
				f, err := ioutil.TempFile("", tt.inputFile)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.Write(tt.inputContents)
				f.Close()
				t.Cleanup(func() { os.Remove(f.Name()) })
				inputFile = f.Name()
			}

			var bodyBytes []byte
			options := ApiOptions{
				RequestPath:      "hello",
				RequestInputFile: inputFile,
				RawFields:        []string{"a=b", "c=d"},

				IO: io,
				HttpClient: func() (*http.Client, error) {
					var tr roundTripper = func(req *http.Request) (*http.Response, error) {
						var err error
						if bodyBytes, err = ioutil.ReadAll(req.Body); err != nil {
							return nil, err
						}
						resp.Request = req
						return resp, nil
					}
					return &http.Client{Transport: tr}, nil
				},
			}

			err := apiRun(&options)
			if err != nil {
				t.Errorf("got error %v", err)
			}

			assert.Equal(t, "POST", resp.Request.Method)
			assert.Equal(t, "/hello?a=b&c=d", resp.Request.URL.RequestURI())
			assert.Equal(t, tt.contentLength, resp.Request.ContentLength)
			assert.Equal(t, "", resp.Request.Header.Get("Content-Type"))
			assert.Equal(t, tt.inputContents, bodyBytes)
		})
	}
}

func Test_parseFields(t *testing.T) {
	io, stdin, _, _ := iostreams.Test()
	fmt.Fprint(stdin, "pasted contents")

	opts := ApiOptions{
		IO: io,
		RawFields: []string{
			"robot=Hubot",
			"destroyer=false",
			"helper=true",
			"location=@work",
		},
		MagicFields: []string{
			"input=@-",
			"enabled=true",
			"victories=123",
		},
	}

	params, err := parseFields(&opts)
	if err != nil {
		t.Fatalf("parseFields error: %v", err)
	}

	expect := map[string]interface{}{
		"robot":     "Hubot",
		"destroyer": "false",
		"helper":    "true",
		"location":  "@work",
		"input":     []byte("pasted contents"),
		"enabled":   true,
		"victories": 123,
	}
	assert.Equal(t, expect, params)
}

func Test_magicFieldValue(t *testing.T) {
	f, err := ioutil.TempFile("", "gh-test")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "file contents")
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	io, _, _, _ := iostreams.Test()

	type args struct {
		v    string
		opts *ApiOptions
	}
	tests := []struct {
		name    string
		args    args
		want    interface{}
		wantErr bool
	}{
		{
			name:    "string",
			args:    args{v: "hello"},
			want:    "hello",
			wantErr: false,
		},
		{
			name:    "bool true",
			args:    args{v: "true"},
			want:    true,
			wantErr: false,
		},
		{
			name:    "bool false",
			args:    args{v: "false"},
			want:    false,
			wantErr: false,
		},
		{
			name:    "null",
			args:    args{v: "null"},
			want:    nil,
			wantErr: false,
		},
		{
			name: "placeholder",
			args: args{
				v: ":owner",
				opts: &ApiOptions{
					IO: io,
					BaseRepo: func() (ghrepo.Interface, error) {
						return ghrepo.New("hubot", "robot-uprising"), nil
					},
				},
			},
			want:    "hubot",
			wantErr: false,
		},
		{
			name: "file",
			args: args{
				v:    "@" + f.Name(),
				opts: &ApiOptions{IO: io},
			},
			want:    []byte("file contents"),
			wantErr: false,
		},
		{
			name: "file error",
			args: args{
				v:    "@",
				opts: &ApiOptions{IO: io},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := magicFieldValue(tt.args.v, tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("magicFieldValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_openUserFile(t *testing.T) {
	f, err := ioutil.TempFile("", "gh-test")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(f, "file contents")
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	file, length, err := openUserFile(f.Name(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	fb, err := ioutil.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, int64(13), length)
	assert.Equal(t, "file contents", string(fb))
}

func Test_fillPlaceholders(t *testing.T) {
	type args struct {
		value string
		opts  *ApiOptions
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "no changes",
			args: args{
				value: "repos/owner/repo/releases",
				opts: &ApiOptions{
					BaseRepo: nil,
				},
			},
			want:    "repos/owner/repo/releases",
			wantErr: false,
		},
		{
			name: "has substitutes",
			args: args{
				value: "repos/:owner/:repo/releases",
				opts: &ApiOptions{
					BaseRepo: func() (ghrepo.Interface, error) {
						return ghrepo.New("hubot", "robot-uprising"), nil
					},
				},
			},
			want:    "repos/hubot/robot-uprising/releases",
			wantErr: false,
		},
		{
			name: "no greedy substitutes",
			args: args{
				value: ":ownership/:repository",
				opts: &ApiOptions{
					BaseRepo: nil,
				},
			},
			want:    ":ownership/:repository",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fillPlaceholders(tt.args.value, tt.args.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("fillPlaceholders() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("fillPlaceholders() got = %v, want %v", got, tt.want)
			}
		})
	}
}
