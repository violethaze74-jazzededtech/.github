package sync

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/cli/cli/v2/context"
	"github.com/cli/cli/v2/git"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/httpmock"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func TestNewCmdSync(t *testing.T) {
	tests := []struct {
		name    string
		tty     bool
		input   string
		output  SyncOptions
		wantErr bool
		errMsg  string
	}{
		{
			name:   "no argument",
			tty:    true,
			input:  "",
			output: SyncOptions{},
		},
		{
			name:  "destination repo",
			tty:   true,
			input: "cli/cli",
			output: SyncOptions{
				DestArg: "cli/cli",
			},
		},
		{
			name:  "source repo",
			tty:   true,
			input: "--source cli/cli",
			output: SyncOptions{
				SrcArg: "cli/cli",
			},
		},
		{
			name:  "branch",
			tty:   true,
			input: "--branch trunk",
			output: SyncOptions{
				Branch: "trunk",
			},
		},
		{
			name:  "force",
			tty:   true,
			input: "--force",
			output: SyncOptions{
				Force: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := iostreams.Test()
			io.SetStdinTTY(tt.tty)
			io.SetStdoutTTY(tt.tty)
			f := &cmdutil.Factory{
				IOStreams: io,
			}
			argv, err := shlex.Split(tt.input)
			assert.NoError(t, err)
			var gotOpts *SyncOptions
			cmd := NewCmdSync(f, func(opts *SyncOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			_, err = cmd.ExecuteC()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.output.DestArg, gotOpts.DestArg)
			assert.Equal(t, tt.output.SrcArg, gotOpts.SrcArg)
			assert.Equal(t, tt.output.Branch, gotOpts.Branch)
			assert.Equal(t, tt.output.Force, gotOpts.Force)
		})
	}
}

func Test_SyncRun(t *testing.T) {
	tests := []struct {
		name       string
		tty        bool
		opts       *SyncOptions
		remotes    []*context.Remote
		httpStubs  func(*httpmock.Registry)
		gitStubs   func(*mockGitClient)
		wantStdout string
		wantErr    bool
		errMsg     string
	}{
		{
			name: "sync local repo with parent - tty",
			tty:  true,
			opts: &SyncOptions{},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("IsDirty").Return(false, nil).Once()
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(true, nil).Once()
				mgc.On("CurrentBranch").Return("trunk", nil).Once()
				mgc.On("MergeFastForward", "FETCH_HEAD").Return(nil).Once()
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to local repository\n",
		},
		{
			name: "sync local repo with parent - notty",
			tty:  false,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("IsDirty").Return(false, nil).Once()
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(true, nil).Once()
				mgc.On("CurrentBranch").Return("trunk", nil).Once()
				mgc.On("MergeFastForward", "FETCH_HEAD").Return(nil).Once()
			},
			wantStdout: "",
		},
		{
			name: "sync local repo with specified source repo",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
				SrcArg: "OWNER2/REPO2",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("IsDirty").Return(false, nil).Once()
				mgc.On("Fetch", "upstream", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("upstream", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(true, nil).Once()
				mgc.On("CurrentBranch").Return("trunk", nil).Once()
				mgc.On("MergeFastForward", "FETCH_HEAD").Return(nil).Once()
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER2/REPO2 to local repository\n",
		},
		{
			name: "sync local repo with parent and force specified",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
				Force:  true,
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("IsDirty").Return(false, nil).Once()
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(false, nil).Once()
				mgc.On("CurrentBranch").Return("trunk", nil).Once()
				mgc.On("ResetHard", "FETCH_HEAD").Return(nil).Once()
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to local repository\n",
		},
		{
			name: "sync local repo with parent and not fast forward merge",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(false, nil).Once()
			},
			wantErr: true,
			errMsg:  "can't sync because there are diverging changes; use `--force` to overwrite the destination branch",
		},
		{
			name: "sync local repo with parent and mismatching branch remotes",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("upstream", nil).Once()
			},
			wantErr: true,
			errMsg:  "can't sync because trunk is not tracking OWNER/REPO",
		},
		{
			name: "sync local repo with parent and local changes",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(true, nil).Once()
				mgc.On("CurrentBranch").Return("trunk", nil).Once()
				mgc.On("IsDirty").Return(true, nil).Once()
			},
			wantErr: true,
			errMsg:  "can't sync because there are local changes; please stash them before trying again",
		},
		{
			name: "sync local repo with parent - existing branch, non-current",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(true).Once()
				mgc.On("BranchRemote", "trunk").Return("origin", nil).Once()
				mgc.On("IsAncestor", "trunk", "FETCH_HEAD").Return(true, nil).Once()
				mgc.On("CurrentBranch").Return("test", nil).Once()
				mgc.On("UpdateBranch", "trunk", "FETCH_HEAD").Return(nil).Once()
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to local repository\n",
		},
		{
			name: "sync local repo with parent - create new branch",
			tty:  true,
			opts: &SyncOptions{
				Branch: "trunk",
			},
			gitStubs: func(mgc *mockGitClient) {
				mgc.On("Fetch", "origin", "refs/heads/trunk").Return(nil).Once()
				mgc.On("HasLocalBranch", "trunk").Return(false).Once()
				mgc.On("CurrentBranch").Return("test", nil).Once()
				mgc.On("CreateBranch", "trunk", "FETCH_HEAD", "origin/trunk").Return(nil).Once()
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to local repository\n",
		},
		{
			name: "sync remote fork with parent - tty",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO-FORK",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{"parent":{"name":"REPO","owner":{"login": "OWNER"}}}}}`))
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/git/refs/heads/trunk"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO-FORK/git/refs/heads/trunk"),
					httpmock.StringResponse(`{}`))
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to OWNER/REPO-FORK\n",
		},
		{
			name: "sync remote fork with parent - notty",
			tty:  false,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO-FORK",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{"parent":{"name":"REPO","owner":{"login": "OWNER"}}}}}`))
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/git/refs/heads/trunk"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO-FORK/git/refs/heads/trunk"),
					httpmock.StringResponse(`{}`))
			},
			wantStdout: "",
		},
		{
			name: "sync remote repo with no parent",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{}}}`))
			},
			wantErr: true,
			errMsg:  "can't determine source repository for OWNER/REPO because repository is not fork",
		},
		{
			name: "sync remote repo with specified source repo",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO",
				SrcArg:  "OWNER2/REPO2",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER2/REPO2/git/refs/heads/trunk"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO/git/refs/heads/trunk"),
					httpmock.StringResponse(`{}`))
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER2/REPO2 to OWNER/REPO\n",
		},
		{
			name: "sync remote fork with parent and specified branch",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO-FORK",
				Branch:  "test",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{"parent":{"name":"REPO","owner":{"login": "OWNER"}}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/git/refs/heads/test"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO-FORK/git/refs/heads/test"),
					httpmock.StringResponse(`{}`))
			},
			wantStdout: "✓ Synced the \"test\" branch from OWNER/REPO to OWNER/REPO-FORK\n",
		},
		{
			name: "sync remote fork with parent and force specified",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO-FORK",
				Force:   true,
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{"parent":{"name":"REPO","owner":{"login": "OWNER"}}}}}`))
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/git/refs/heads/trunk"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO-FORK/git/refs/heads/trunk"),
					httpmock.StringResponse(`{}`))
			},
			wantStdout: "✓ Synced the \"trunk\" branch from OWNER/REPO to OWNER/REPO-FORK\n",
		},
		{
			name: "sync remote fork with parent and not fast forward merge",
			tty:  true,
			opts: &SyncOptions{
				DestArg: "OWNER/REPO-FORK",
			},
			httpStubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.GraphQL(`query RepositoryFindParent\b`),
					httpmock.StringResponse(`{"data":{"repository":{"parent":{"name":"REPO","owner":{"login": "OWNER"}}}}}`))
				reg.Register(
					httpmock.GraphQL(`query RepositoryInfo\b`),
					httpmock.StringResponse(`{"data":{"repository":{"defaultBranchRef":{"name": "trunk"}}}}`))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/git/refs/heads/trunk"),
					httpmock.StringResponse(`{"object":{"sha":"0xDEADBEEF"}}`))
				reg.Register(
					httpmock.REST("PATCH", "repos/OWNER/REPO-FORK/git/refs/heads/trunk"),
					func(req *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: 422,
							Request:    req,
							Header:     map[string][]string{"Content-Type": {"application/json"}},
							Body:       ioutil.NopCloser(bytes.NewBufferString(`{"message":"Update is not a fast forward"}`)),
						}, nil
					})
			},
			wantErr: true,
			errMsg:  "can't sync because there are diverging changes; use `--force` to overwrite the destination branch",
		},
	}
	for _, tt := range tests {
		reg := &httpmock.Registry{}
		if tt.httpStubs != nil {
			tt.httpStubs(reg)
		}
		tt.opts.HttpClient = func() (*http.Client, error) {
			return &http.Client{Transport: reg}, nil
		}

		io, _, stdout, _ := iostreams.Test()
		io.SetStdinTTY(tt.tty)
		io.SetStdoutTTY(tt.tty)
		tt.opts.IO = io

		repo1, _ := ghrepo.FromFullName("OWNER/REPO")
		repo2, _ := ghrepo.FromFullName("OWNER2/REPO2")
		tt.opts.BaseRepo = func() (ghrepo.Interface, error) {
			return repo1, nil
		}

		tt.opts.Remotes = func() (context.Remotes, error) {
			if tt.remotes == nil {
				return []*context.Remote{
					{
						Remote: &git.Remote{Name: "origin"},
						Repo:   repo1,
					},
					{
						Remote: &git.Remote{Name: "upstream"},
						Repo:   repo2,
					},
				}, nil
			}
			return tt.remotes, nil
		}

		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Git = newMockGitClient(t, tt.gitStubs)
			defer reg.Verify(t)
			err := syncRun(tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, tt.errMsg, err.Error())
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.wantStdout, stdout.String())
		})
	}
}

func newMockGitClient(t *testing.T, config func(*mockGitClient)) *mockGitClient {
	t.Helper()
	m := &mockGitClient{}
	m.Test(t)
	t.Cleanup(func() {
		t.Helper()
		m.AssertExpectations(t)
	})
	if config != nil {
		config(m)
	}
	return m
}
