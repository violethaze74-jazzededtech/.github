package comment

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/cli/cli/context"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/pr/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func TestNewCmdComment(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		output   shared.CommentableOptions
		wantsErr bool
	}{
		{
			name:  "no arguments",
			input: "",
			output: shared.CommentableOptions{
				Interactive: true,
				InputType:   0,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:     "two arguments",
			input:    "1 2",
			output:   shared.CommentableOptions{},
			wantsErr: true,
		},
		{
			name:  "pr number",
			input: "1",
			output: shared.CommentableOptions{
				Interactive: true,
				InputType:   0,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:  "pr url",
			input: "https://github.com/OWNER/REPO/pull/12",
			output: shared.CommentableOptions{
				Interactive: true,
				InputType:   0,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:  "pr branch",
			input: "branch-name",
			output: shared.CommentableOptions{
				Interactive: true,
				InputType:   0,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:  "body flag",
			input: "1 --body test",
			output: shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeInline,
				Body:        "test",
			},
			wantsErr: false,
		},
		{
			name:  "editor flag",
			input: "1 --editor",
			output: shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeEditor,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:  "web flag",
			input: "1 --web",
			output: shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeWeb,
				Body:        "",
			},
			wantsErr: false,
		},
		{
			name:     "editor and web flags",
			input:    "1 --editor --web",
			output:   shared.CommentableOptions{},
			wantsErr: true,
		},
		{
			name:     "editor and body flags",
			input:    "1 --editor --body test",
			output:   shared.CommentableOptions{},
			wantsErr: true,
		},
		{
			name:     "web and body flags",
			input:    "1 --web --body test",
			output:   shared.CommentableOptions{},
			wantsErr: true,
		},
		{
			name:     "editor, web, and body flags",
			input:    "1 --editor --web --body test",
			output:   shared.CommentableOptions{},
			wantsErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := iostreams.Test()
			io.SetStdoutTTY(true)
			io.SetStdinTTY(true)
			io.SetStderrTTY(true)

			f := &cmdutil.Factory{
				IOStreams: io,
			}

			argv, err := shlex.Split(tt.input)
			assert.NoError(t, err)

			var gotOpts *shared.CommentableOptions
			cmd := NewCmdComment(f, func(opts *shared.CommentableOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.Flags().BoolP("help", "x", false, "")

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
			assert.Equal(t, tt.output.Interactive, gotOpts.Interactive)
			assert.Equal(t, tt.output.InputType, gotOpts.InputType)
			assert.Equal(t, tt.output.Body, gotOpts.Body)
		})
	}
}

func Test_commentRun(t *testing.T) {
	tests := []struct {
		name      string
		input     *shared.CommentableOptions
		httpStubs func(*testing.T, *httpmock.Registry)
		stdout    string
		stderr    string
	}{
		{
			name: "interactive editor",
			input: &shared.CommentableOptions{
				Interactive: true,
				InputType:   0,
				Body:        "",

				InteractiveEditSurvey: func() (string, error) { return "comment body", nil },
				ConfirmSubmitSurvey:   func() (bool, error) { return true, nil },
			},
			httpStubs: func(t *testing.T, reg *httpmock.Registry) {
				mockPullRequestFromNumber(t, reg)
				mockCommentCreate(t, reg)
			},
			stdout: "https://github.com/OWNER/REPO/pull/123#issuecomment-456\n",
		},
		{
			name: "non-interactive web",
			input: &shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeWeb,
				Body:        "",

				OpenInBrowser: func(string) error { return nil },
			},
			httpStubs: func(t *testing.T, reg *httpmock.Registry) {
				mockPullRequestFromNumber(t, reg)
			},
			stderr: "Opening github.com/OWNER/REPO/pull/123 in your browser.\n",
		},
		{
			name: "non-interactive editor",
			input: &shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeEditor,
				Body:        "",

				EditSurvey: func() (string, error) { return "comment body", nil },
			},
			httpStubs: func(t *testing.T, reg *httpmock.Registry) {
				mockPullRequestFromNumber(t, reg)
				mockCommentCreate(t, reg)
			},
			stdout: "https://github.com/OWNER/REPO/pull/123#issuecomment-456\n",
		},
		{
			name: "non-interactive inline",
			input: &shared.CommentableOptions{
				Interactive: false,
				InputType:   shared.InputTypeInline,
				Body:        "comment body",
			},
			httpStubs: func(t *testing.T, reg *httpmock.Registry) {
				mockPullRequestFromNumber(t, reg)
				mockCommentCreate(t, reg)
			},
			stdout: "https://github.com/OWNER/REPO/pull/123#issuecomment-456\n",
		},
	}
	for _, tt := range tests {
		io, _, stdout, stderr := iostreams.Test()
		io.SetStdoutTTY(true)
		io.SetStdinTTY(true)
		io.SetStderrTTY(true)

		reg := &httpmock.Registry{}
		defer reg.Verify(t)
		tt.httpStubs(t, reg)

		httpClient := func() (*http.Client, error) { return &http.Client{Transport: reg}, nil }
		baseRepo := func() (ghrepo.Interface, error) { return ghrepo.New("OWNER", "REPO"), nil }
		branch := func() (string, error) { return "", nil }
		remotes := func() (context.Remotes, error) { return nil, nil }

		tt.input.IO = io
		tt.input.HttpClient = httpClient
		tt.input.RetrieveCommentable = retrievePR(httpClient, baseRepo, branch, remotes, "123")

		t.Run(tt.name, func(t *testing.T) {
			err := shared.CommentableRun(tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.stdout, stdout.String())
			assert.Equal(t, tt.stderr, stderr.String())
		})
	}
}

func mockPullRequestFromNumber(_ *testing.T, reg *httpmock.Registry) {
	reg.Register(
		httpmock.GraphQL(`query PullRequestByNumber\b`),
		httpmock.StringResponse(`
			{ "data": { "repository": { "pullRequest": {
				"number": 123,
				"url": "https://github.com/OWNER/REPO/pull/123"
			} } } }`),
	)
}

func mockCommentCreate(t *testing.T, reg *httpmock.Registry) {
	reg.Register(
		httpmock.GraphQL(`mutation CommentCreate\b`),
		httpmock.GraphQLMutation(`
		{ "data": { "addComment": { "commentEdge": { "node": {
			"url": "https://github.com/OWNER/REPO/pull/123#issuecomment-456"
		} } } } }`,
			func(inputs map[string]interface{}) {
				assert.Equal(t, "comment body", inputs["body"])
			}),
	)
}
