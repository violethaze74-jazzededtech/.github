package edit

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/pkg/cmd/gist/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func TestNewCmdEdit(t *testing.T) {
	tests := []struct {
		name  string
		cli   string
		wants EditOptions
	}{
		{
			name: "no flags",
			cli:  "123",
			wants: EditOptions{
				Selector: "123",
			},
		},
		{
			name: "filename",
			cli:  "123 --filename cool.md",
			wants: EditOptions{
				Selector: "123",
				Filename: "cool.md",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &cmdutil.Factory{}

			argv, err := shlex.Split(tt.cli)
			assert.NoError(t, err)

			var gotOpts *EditOptions
			cmd := NewCmdEdit(f, func(opts *EditOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			assert.NoError(t, err)

			assert.Equal(t, tt.wants.Filename, gotOpts.Filename)
			assert.Equal(t, tt.wants.Selector, gotOpts.Selector)
		})
	}
}

func Test_editRun(t *testing.T) {
	tests := []struct {
		name       string
		opts       *EditOptions
		gist       *shared.Gist
		httpStubs  func(*httpmock.Registry)
		askStubs   func(*prompt.AskStubber)
		nontty     bool
		wantErr    bool
		wantStderr string
		wantParams map[string]interface{}
	}{
		{
			name:    "no such gist",
			wantErr: true,
		},
		{
			name: "one file",
			gist: &shared.Gist{
				ID: "1234",
				Files: map[string]*shared.GistFile{
					"cicada.txt": {
						Filename: "cicada.txt",
						Content:  "bwhiizzzbwhuiiizzzz",
						Type:     "text/plain",
					},
				},
				Owner: &shared.GistOwner{Login: "octocat"},
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(httpmock.REST("POST", "gists/1234"),
					httpmock.StatusStringResponse(201, "{}"))
			},
			wantParams: map[string]interface{}{
				"description": "",
				"updated_at":  "0001-01-01T00:00:00Z",
				"public":      false,
				"files": map[string]interface{}{
					"cicada.txt": map[string]interface{}{
						"content":  "new file content",
						"filename": "cicada.txt",
						"type":     "text/plain",
					},
				},
			},
		},
		{
			name: "multiple files, submit",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("unix.md")
				as.StubOne("Submit")
			},
			gist: &shared.Gist{
				ID:          "1234",
				Description: "catbug",
				Files: map[string]*shared.GistFile{
					"cicada.txt": {
						Filename: "cicada.txt",
						Content:  "bwhiizzzbwhuiiizzzz",
						Type:     "text/plain",
					},
					"unix.md": {
						Filename: "unix.md",
						Content:  "meow",
						Type:     "application/markdown",
					},
				},
				Owner: &shared.GistOwner{Login: "octocat"},
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(httpmock.REST("POST", "gists/1234"),
					httpmock.StatusStringResponse(201, "{}"))
			},
			wantParams: map[string]interface{}{
				"description": "catbug",
				"updated_at":  "0001-01-01T00:00:00Z",
				"public":      false,
				"files": map[string]interface{}{
					"cicada.txt": map[string]interface{}{
						"content":  "bwhiizzzbwhuiiizzzz",
						"filename": "cicada.txt",
						"type":     "text/plain",
					},
					"unix.md": map[string]interface{}{
						"content":  "new file content",
						"filename": "unix.md",
						"type":     "application/markdown",
					},
				},
			},
		},
		{
			name: "multiple files, cancel",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("unix.md")
				as.StubOne("Cancel")
			},
			wantErr: true,
			gist: &shared.Gist{
				ID: "1234",
				Files: map[string]*shared.GistFile{
					"cicada.txt": {
						Filename: "cicada.txt",
						Content:  "bwhiizzzbwhuiiizzzz",
						Type:     "text/plain",
					},
					"unix.md": {
						Filename: "unix.md",
						Content:  "meow",
						Type:     "application/markdown",
					},
				},
				Owner: &shared.GistOwner{Login: "octocat"},
			},
		},
		{
			name: "not change",
			gist: &shared.Gist{
				ID: "1234",
				Files: map[string]*shared.GistFile{
					"cicada.txt": {
						Filename: "cicada.txt",
						Content:  "new file content",
						Type:     "text/plain",
					},
				},
				Owner: &shared.GistOwner{Login: "octocat"},
			},
		},
		{
			name: "another user's gist",
			gist: &shared.Gist{
				ID: "1234",
				Files: map[string]*shared.GistFile{
					"cicada.txt": {
						Filename: "cicada.txt",
						Content:  "bwhiizzzbwhuiiizzzz",
						Type:     "text/plain",
					},
				},
				Owner: &shared.GistOwner{Login: "octocat2"},
			},
			wantErr:    true,
			wantStderr: "You do not own this gist.",
		},
	}

	for _, tt := range tests {
		reg := &httpmock.Registry{}
		if tt.gist == nil {
			reg.Register(httpmock.REST("GET", "gists/1234"),
				httpmock.StatusStringResponse(404, "Not Found"))
		} else {
			reg.Register(httpmock.REST("GET", "gists/1234"),
				httpmock.JSONResponse(tt.gist))
			reg.Register(httpmock.GraphQL(`query UserCurrent\b`),
				httpmock.StringResponse(`{"data":{"viewer":{"login":"octocat"}}}`))
		}

		if tt.httpStubs != nil {
			tt.httpStubs(reg)
		}

		as, teardown := prompt.InitAskStubber()
		defer teardown()
		if tt.askStubs != nil {
			tt.askStubs(as)
		}

		if tt.opts == nil {
			tt.opts = &EditOptions{}
		}

		tt.opts.Edit = func(_, _, _ string, _ *iostreams.IOStreams) (string, error) {
			return "new file content", nil
		}

		tt.opts.HttpClient = func() (*http.Client, error) {
			return &http.Client{Transport: reg}, nil
		}
		io, _, _, _ := iostreams.Test()
		io.SetStdoutTTY(!tt.nontty)
		io.SetStdinTTY(!tt.nontty)
		tt.opts.IO = io

		tt.opts.Selector = "1234"

		tt.opts.Config = func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		}

		t.Run(tt.name, func(t *testing.T) {
			err := editRun(tt.opts)
			reg.Verify(t)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantStderr != "" {
					assert.EqualError(t, err, tt.wantStderr)
				}
				return
			}
			assert.NoError(t, err)

			if tt.wantParams != nil {
				bodyBytes, _ := ioutil.ReadAll(reg.Requests[2].Body)
				reqBody := make(map[string]interface{})
				err = json.Unmarshal(bodyBytes, &reqBody)
				if err != nil {
					t.Fatalf("error decoding JSON: %v", err)
				}
				assert.Equal(t, tt.wantParams, reqBody)
			}
		})
	}
}
