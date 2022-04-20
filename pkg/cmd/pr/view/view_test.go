package view

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/briandowns/spinner"
	"github.com/cli/cli/context"
	"github.com/cli/cli/git"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/internal/run"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/test"
	"github.com/cli/cli/utils"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewCmdView(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		isTTY   bool
		want    ViewOptions
		wantErr string
	}{
		{
			name:  "number argument",
			args:  "123",
			isTTY: true,
			want: ViewOptions{
				SelectorArg: "123",
				BrowserMode: false,
			},
		},
		{
			name:  "no argument",
			args:  "",
			isTTY: true,
			want: ViewOptions{
				SelectorArg: "",
				BrowserMode: false,
			},
		},
		{
			name:  "web mode",
			args:  "123 -w",
			isTTY: true,
			want: ViewOptions{
				SelectorArg: "123",
				BrowserMode: true,
			},
		},
		{
			name:    "no argument with --repo override",
			args:    "-R owner/repo",
			isTTY:   true,
			wantErr: "argument required when using the --repo flag",
		},
		{
			name:  "comments",
			args:  "123 -c",
			isTTY: true,
			want: ViewOptions{
				SelectorArg: "123",
				Comments:    true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := iostreams.Test()
			io.SetStdoutTTY(tt.isTTY)
			io.SetStdinTTY(tt.isTTY)
			io.SetStderrTTY(tt.isTTY)

			f := &cmdutil.Factory{
				IOStreams: io,
			}

			var opts *ViewOptions
			cmd := NewCmdView(f, func(o *ViewOptions) error {
				opts = o
				return nil
			})
			cmd.PersistentFlags().StringP("repo", "R", "", "")

			argv, err := shlex.Split(tt.args)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(ioutil.Discard)
			cmd.SetErr(ioutil.Discard)

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.want.SelectorArg, opts.SelectorArg)
		})
	}
}

func runCommand(rt http.RoundTripper, branch string, isTTY bool, cli string) (*test.CmdOut, error) {
	io, _, stdout, stderr := iostreams.Test()
	io.SetStdoutTTY(isTTY)
	io.SetStdinTTY(isTTY)
	io.SetStderrTTY(isTTY)

	factory := &cmdutil.Factory{
		IOStreams: io,
		HttpClient: func() (*http.Client, error) {
			return &http.Client{Transport: rt}, nil
		},
		Config: func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		},
		BaseRepo: func() (ghrepo.Interface, error) {
			return ghrepo.New("OWNER", "REPO"), nil
		},
		Remotes: func() (context.Remotes, error) {
			return context.Remotes{
				{
					Remote: &git.Remote{Name: "origin"},
					Repo:   ghrepo.New("OWNER", "REPO"),
				},
			}, nil
		},
		Branch: func() (string, error) {
			return branch, nil
		},
	}

	cmd := NewCmdView(factory, nil)

	argv, err := shlex.Split(cli)
	if err != nil {
		return nil, err
	}
	cmd.SetArgs(argv)

	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(ioutil.Discard)
	cmd.SetErr(ioutil.Discard)

	_, err = cmd.ExecuteC()
	return &test.CmdOut{
		OutBuf: stdout,
		ErrBuf: stderr,
	}, err
}

func TestPRView_Preview_nontty(t *testing.T) {
	tests := map[string]struct {
		branch          string
		args            string
		fixture         string
		expectedOutputs []string
	}{
		"Open PR without metadata": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreview.json",
			expectedOutputs: []string{
				`title:\tBlueberries are from a fork\n`,
				`state:\tOPEN\n`,
				`author:\tnobody\n`,
				`labels:\t\n`,
				`assignees:\t\n`,
				`reviewers:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
				`url:\thttps://github.com/OWNER/REPO/pull/12\n`,
				`number:\t12\n`,
				`blueberries taste good`,
			},
		},
		"Open PR with metadata by number": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewWithMetadataByNumber.json",
			expectedOutputs: []string{
				`title:\tBlueberries are from a fork\n`,
				`reviewers:\t2 \(Approved\), 3 \(Commented\), 1 \(Requested\)\n`,
				`assignees:\tmarseilles, monaco\n`,
				`labels:\tone, two, three, four, five\n`,
				`projects:\tProject 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\), Project 4 \(Awaiting triage\)\n`,
				`milestone:\tuluru\n`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Open PR with reviewers by number": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewWithReviewersByNumber.json",
			expectedOutputs: []string{
				`title:\tBlueberries are from a fork\n`,
				`state:\tOPEN\n`,
				`author:\tnobody\n`,
				`labels:\t\n`,
				`assignees:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
				`reviewers:\tDEF \(Commented\), def \(Changes requested\), ghost \(Approved\), hubot \(Commented\), xyz \(Approved\), 123 \(Requested\), Team 1 \(Requested\), abc \(Requested\)\n`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Open PR with metadata by branch": {
			branch:  "master",
			args:    "blueberries",
			fixture: "./fixtures/prViewPreviewWithMetadataByBranch.json",
			expectedOutputs: []string{
				`title:\tBlueberries are a good fruit`,
				`state:\tOPEN`,
				`author:\tnobody`,
				`assignees:\tmarseilles, monaco\n`,
				`labels:\tone, two, three, four, five\n`,
				`projects:\tProject 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\)\n`,
				`milestone:\tuluru\n`,
				`blueberries taste good`,
			},
		},
		"Open PR for the current branch": {
			branch:  "blueberries",
			args:    "",
			fixture: "./fixtures/prView.json",
			expectedOutputs: []string{
				`title:\tBlueberries are a good fruit`,
				`state:\tOPEN`,
				`author:\tnobody`,
				`assignees:\t\n`,
				`labels:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Open PR wth empty body for the current branch": {
			branch:  "blueberries",
			args:    "",
			fixture: "./fixtures/prView_EmptyBody.json",
			expectedOutputs: []string{
				`title:\tBlueberries are a good fruit`,
				`state:\tOPEN`,
				`author:\tnobody`,
				`assignees:\t\n`,
				`labels:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
			},
		},
		"Closed PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewClosedState.json",
			expectedOutputs: []string{
				`state:\tCLOSED\n`,
				`author:\tnobody\n`,
				`labels:\t\n`,
				`assignees:\t\n`,
				`reviewers:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Merged PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewMergedState.json",
			expectedOutputs: []string{
				`state:\tMERGED\n`,
				`author:\tnobody\n`,
				`labels:\t\n`,
				`assignees:\t\n`,
				`reviewers:\t\n`,
				`projects:\t\n`,
				`milestone:\t\n`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Draft PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewDraftState.json",
			expectedOutputs: []string{
				`title:\tBlueberries are from a fork\n`,
				`state:\tDRAFT\n`,
				`author:\tnobody\n`,
				`labels:`,
				`assignees:`,
				`projects:`,
				`milestone:`,
				`\*\*blueberries taste good\*\*`,
			},
		},
		"Draft PR by branch": {
			branch:  "master",
			args:    "blueberries",
			fixture: "./fixtures/prViewPreviewDraftStatebyBranch.json",
			expectedOutputs: []string{
				`title:\tBlueberries are a good fruit\n`,
				`state:\tDRAFT\n`,
				`author:\tnobody\n`,
				`labels:`,
				`assignees:`,
				`projects:`,
				`milestone:`,
				`\*\*blueberries taste good\*\*`,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			http := &httpmock.Registry{}
			defer http.Verify(t)
			http.Register(httpmock.GraphQL(`query PullRequest(ByNumber|ForBranch)\b`), httpmock.FileResponse(tc.fixture))

			output, err := runCommand(http, tc.branch, false, tc.args)
			if err != nil {
				t.Errorf("error running command `%v`: %v", tc.args, err)
			}

			assert.Equal(t, "", output.Stderr())

			test.ExpectLines(t, output.String(), tc.expectedOutputs...)
		})
	}
}

func TestPRView_Preview(t *testing.T) {
	tests := map[string]struct {
		branch          string
		args            string
		fixture         string
		expectedOutputs []string
	}{
		"Open PR without metadata": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreview.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Open.*nobody wants to merge 12 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Open PR with metadata by number": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewWithMetadataByNumber.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Open.*nobody wants to merge 12 commits into master from blueberries`,
				`Reviewers:.*2 \(.*Approved.*\), 3 \(Commented\), 1 \(.*Requested.*\)\n`,
				`Assignees:.*marseilles, monaco\n`,
				`Labels:.*one, two, three, four, five\n`,
				`Projects:.*Project 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\), Project 4 \(Awaiting triage\)\n`,
				`Milestone:.*uluru\n`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Open PR with reviewers by number": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewWithReviewersByNumber.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Reviewers:.*DEF \(.*Commented.*\), def \(.*Changes requested.*\), ghost \(.*Approved.*\), hubot \(Commented\), xyz \(.*Approved.*\), 123 \(.*Requested.*\), Team 1 \(.*Requested.*\), abc \(.*Requested.*\)\n`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Open PR with metadata by branch": {
			branch:  "master",
			args:    "blueberries",
			fixture: "./fixtures/prViewPreviewWithMetadataByBranch.json",
			expectedOutputs: []string{
				`Blueberries are a good fruit`,
				`Open.*nobody wants to merge 8 commits into master from blueberries`,
				`Assignees:.*marseilles, monaco\n`,
				`Labels:.*one, two, three, four, five\n`,
				`Projects:.*Project 1 \(column A\), Project 2 \(column B\), Project 3 \(column C\)\n`,
				`Milestone:.*uluru\n`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/10`,
			},
		},
		"Open PR for the current branch": {
			branch:  "blueberries",
			args:    "",
			fixture: "./fixtures/prView.json",
			expectedOutputs: []string{
				`Blueberries are a good fruit`,
				`Open.*nobody wants to merge 8 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/10`,
			},
		},
		"Open PR wth empty body for the current branch": {
			branch:  "blueberries",
			args:    "",
			fixture: "./fixtures/prView_EmptyBody.json",
			expectedOutputs: []string{
				`Blueberries are a good fruit`,
				`Open.*nobody wants to merge 8 commits into master from blueberries`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/10`,
			},
		},
		"Closed PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewClosedState.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Closed.*nobody wants to merge 12 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Merged PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewMergedState.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Merged.*nobody wants to merge 12 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Draft PR": {
			branch:  "master",
			args:    "12",
			fixture: "./fixtures/prViewPreviewDraftState.json",
			expectedOutputs: []string{
				`Blueberries are from a fork`,
				`Draft.*nobody wants to merge 12 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"Draft PR by branch": {
			branch:  "master",
			args:    "blueberries",
			fixture: "./fixtures/prViewPreviewDraftStatebyBranch.json",
			expectedOutputs: []string{
				`Blueberries are a good fruit`,
				`Draft.*nobody wants to merge 8 commits into master from blueberries`,
				`blueberries taste good`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/10`,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			http := &httpmock.Registry{}
			defer http.Verify(t)
			http.Register(httpmock.GraphQL(`query PullRequest(ByNumber|ForBranch)\b`), httpmock.FileResponse(tc.fixture))

			output, err := runCommand(http, tc.branch, true, tc.args)
			if err != nil {
				t.Errorf("error running command `%v`: %v", tc.args, err)
			}

			assert.Equal(t, "", output.Stderr())

			test.ExpectLines(t, output.String(), tc.expectedOutputs...)
		})
	}
}

func TestPRView_web_currentBranch(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.Register(httpmock.GraphQL(`query PullRequestForBranch\b`), httpmock.FileResponse("./fixtures/prView.json"))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		switch strings.Join(cmd.Args, " ") {
		case `git config --get-regexp ^branch\.blueberries\.(remote|merge)$`:
			return &test.OutputStub{}
		default:
			seenCmd = cmd
			return &test.OutputStub{}
		}
	})
	defer restoreCmd()

	output, err := runCommand(http, "blueberries", true, "-w")
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "Opening github.com/OWNER/REPO/pull/10 in your browser.\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	if url != "https://github.com/OWNER/REPO/pull/10" {
		t.Errorf("got: %q", url)
	}
}

func TestPRView_web_noResultsForBranch(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.Register(httpmock.GraphQL(`query PullRequestForBranch\b`), httpmock.FileResponse("./fixtures/prView_NoActiveBranch.json"))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		switch strings.Join(cmd.Args, " ") {
		case `git config --get-regexp ^branch\.blueberries\.(remote|merge)$`:
			return &test.OutputStub{}
		default:
			seenCmd = cmd
			return &test.OutputStub{}
		}
	})
	defer restoreCmd()

	_, err := runCommand(http, "blueberries", true, "-w")
	if err == nil || err.Error() != `no pull requests found for branch "blueberries"` {
		t.Errorf("error running command `pr view`: %v", err)
	}

	if seenCmd != nil {
		t.Fatalf("unexpected command: %v", seenCmd.Args)
	}
}

func TestPRView_web_numberArg(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "pullRequest": {
		"url": "https://github.com/OWNER/REPO/pull/23"
	} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := runCommand(http, "master", true, "-w 23")
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	assert.Equal(t, "https://github.com/OWNER/REPO/pull/23", url)
}

func TestPRView_web_numberArgWithHash(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "pullRequest": {
		"url": "https://github.com/OWNER/REPO/pull/23"
	} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := runCommand(http, "master", true, `-w "#23"`)
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	assert.Equal(t, "https://github.com/OWNER/REPO/pull/23", url)
}

func TestPRView_web_urlArg(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)
	http.StubResponse(200, bytes.NewBufferString(`
		{ "data": { "repository": { "pullRequest": {
			"url": "https://github.com/OWNER/REPO/pull/23"
		} } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := runCommand(http, "master", true, "-w https://github.com/OWNER/REPO/pull/23/files")
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	assert.Equal(t, "https://github.com/OWNER/REPO/pull/23", url)
}

func TestPRView_web_branchArg(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "pullRequests": { "nodes": [
		{ "headRefName": "blueberries",
		  "isCrossRepository": false,
		  "url": "https://github.com/OWNER/REPO/pull/23" }
	] } } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := runCommand(http, "master", true, "-w blueberries")
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	assert.Equal(t, "https://github.com/OWNER/REPO/pull/23", url)
}

func TestPRView_web_branchWithOwnerArg(t *testing.T) {
	http := &httpmock.Registry{}
	defer http.Verify(t)

	http.StubResponse(200, bytes.NewBufferString(`
	{ "data": { "repository": { "pullRequests": { "nodes": [
		{ "headRefName": "blueberries",
		  "isCrossRepository": true,
		  "headRepositoryOwner": { "login": "hubot" },
		  "url": "https://github.com/hubot/REPO/pull/23" }
	] } } } }
	`))

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	output, err := runCommand(http, "master", true, "-w hubot:blueberries")
	if err != nil {
		t.Errorf("error running command `pr view`: %v", err)
	}

	assert.Equal(t, "", output.String())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	url := seenCmd.Args[len(seenCmd.Args)-1]
	assert.Equal(t, "https://github.com/hubot/REPO/pull/23", url)
}

func TestPRView_tty_Comments(t *testing.T) {
	tests := map[string]struct {
		branch          string
		cli             string
		fixtures        map[string]string
		expectedOutputs []string
		wantsErr        bool
	}{
		"without comments flag": {
			branch: "master",
			cli:    "123",
			fixtures: map[string]string{
				"PullRequestByNumber": "./fixtures/prViewPreviewSingleComment.json",
			},
			expectedOutputs: []string{
				`some title`,
				`1 \x{1f615} • 2 \x{1f440} • 3 \x{2764}\x{fe0f}`,
				`some body`,
				`———————— Not showing 4 comments ————————`,
				`marseilles \(collaborator\) • Jan  1, 2020 • Newest comment`,
				`4 \x{1f389} • 5 \x{1f604} • 6 \x{1f680}`,
				`Comment 5`,
				`Use --comments to view the full conversation`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"with comments flag": {
			branch: "master",
			cli:    "123 --comments",
			fixtures: map[string]string{
				"PullRequestByNumber":    "./fixtures/prViewPreviewSingleComment.json",
				"CommentsForPullRequest": "./fixtures/prViewPreviewFullComments.json",
			},
			expectedOutputs: []string{
				`some title`,
				`some body`,
				`monalisa • Jan  1, 2020 • edited`,
				`1 \x{1f615} • 2 \x{1f440} • 3 \x{2764}\x{fe0f} • 4 \x{1f389} • 5 \x{1f604} • 6 \x{1f680} • 7 \x{1f44e} • 8 \x{1f44d}`,
				`Comment 1`,
				`johnnytest \(contributor\) • Jan  1, 2020`,
				`Comment 2`,
				`elvisp \(member\) • Jan  1, 2020`,
				`Comment 3`,
				`loislane \(owner\) • Jan  1, 2020`,
				`Comment 4`,
				`marseilles \(collaborator\) • Jan  1, 2020 • Newest comment`,
				`Comment 5`,
				`View this pull request on GitHub: https://github.com/OWNER/REPO/pull/12`,
			},
		},
		"with invalid comments flag": {
			branch:   "master",
			cli:      "123 --comments 3",
			wantsErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			stubSpinner()
			http := &httpmock.Registry{}
			defer http.Verify(t)
			for name, file := range tt.fixtures {
				name := fmt.Sprintf(`query %s\b`, name)
				http.Register(httpmock.GraphQL(name), httpmock.FileResponse(file))
			}
			output, err := runCommand(http, tt.branch, true, tt.cli)
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, "", output.Stderr())
			test.ExpectLines(t, output.String(), tt.expectedOutputs...)
		})
	}
}

func TestPRView_nontty_Comments(t *testing.T) {
	tests := map[string]struct {
		branch          string
		cli             string
		fixtures        map[string]string
		expectedOutputs []string
		wantsErr        bool
	}{
		"without comments flag": {
			branch: "master",
			cli:    "123",
			fixtures: map[string]string{
				"PullRequestByNumber": "./fixtures/prViewPreviewSingleComment.json",
			},
			expectedOutputs: []string{
				`title:\tsome title`,
				`state:\tOPEN`,
				`author:\tnobody`,
				`url:\thttps://github.com/OWNER/REPO/pull/12`,
				`some body`,
			},
		},
		"with comments flag": {
			branch: "master",
			cli:    "123 --comments",
			fixtures: map[string]string{
				"PullRequestByNumber":    "./fixtures/prViewPreviewSingleComment.json",
				"CommentsForPullRequest": "./fixtures/prViewPreviewFullComments.json",
			},
			expectedOutputs: []string{
				`author:\tmonalisa`,
				`association:\t`,
				`edited:\ttrue`,
				`Comment 1`,
				`author:\tjohnnytest`,
				`association:\tcontributor`,
				`edited:\tfalse`,
				`Comment 2`,
				`author:\telvisp`,
				`association:\tmember`,
				`edited:\tfalse`,
				`Comment 3`,
				`author:\tloislane`,
				`association:\towner`,
				`edited:\tfalse`,
				`Comment 4`,
				`author:\tmarseilles`,
				`association:\tcollaborator`,
				`edited:\tfalse`,
				`Comment 5`,
			},
		},
		"with invalid comments flag": {
			branch:   "master",
			cli:      "123 --comments 3",
			wantsErr: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			http := &httpmock.Registry{}
			defer http.Verify(t)
			for name, file := range tt.fixtures {
				name := fmt.Sprintf(`query %s\b`, name)
				http.Register(httpmock.GraphQL(name), httpmock.FileResponse(file))
			}
			output, err := runCommand(http, tt.branch, false, tt.cli)
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, "", output.Stderr())
			test.ExpectLines(t, output.String(), tt.expectedOutputs...)
		})
	}
}

func stubSpinner() {
	utils.StartSpinner = func(_ *spinner.Spinner) {}
	utils.StopSpinner = func(_ *spinner.Spinner) {}
}
