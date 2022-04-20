package codespace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/v2/internal/codespaces/api"
	"github.com/cli/cli/v2/pkg/iostreams"
)

func TestDelete(t *testing.T) {
	user := &api.User{Login: "hubot"}
	now, _ := time.Parse(time.RFC3339, "2021-09-22T00:00:00Z")
	daysAgo := func(n int) string {
		return now.Add(time.Hour * -time.Duration(24*n)).Format(time.RFC3339)
	}

	tests := []struct {
		name        string
		opts        deleteOptions
		codespaces  []*api.Codespace
		confirms    map[string]bool
		deleteErr   error
		wantErr     bool
		wantDeleted []string
		wantStdout  string
		wantStderr  string
	}{
		{
			name: "by name",
			opts: deleteOptions{
				codespaceName: "hubot-robawt-abc",
			},
			codespaces: []*api.Codespace{
				{
					Name: "hubot-robawt-abc",
				},
			},
			wantDeleted: []string{"hubot-robawt-abc"},
			wantStdout:  "",
		},
		{
			name: "by repo",
			opts: deleteOptions{
				repoFilter: "monalisa/spoon-knife",
			},
			codespaces: []*api.Codespace{
				{
					Name: "monalisa-spoonknife-123",
					Repository: api.Repository{
						FullName: "monalisa/Spoon-Knife",
					},
				},
				{
					Name: "hubot-robawt-abc",
					Repository: api.Repository{
						FullName: "hubot/ROBAWT",
					},
				},
				{
					Name: "monalisa-spoonknife-c4f3",
					Repository: api.Repository{
						FullName: "monalisa/Spoon-Knife",
					},
				},
			},
			wantDeleted: []string{"monalisa-spoonknife-123", "monalisa-spoonknife-c4f3"},
			wantStdout:  "",
		},
		{
			name: "unused",
			opts: deleteOptions{
				deleteAll: true,
				keepDays:  3,
			},
			codespaces: []*api.Codespace{
				{
					Name:       "monalisa-spoonknife-123",
					LastUsedAt: daysAgo(1),
				},
				{
					Name:       "hubot-robawt-abc",
					LastUsedAt: daysAgo(4),
				},
				{
					Name:       "monalisa-spoonknife-c4f3",
					LastUsedAt: daysAgo(10),
				},
			},
			wantDeleted: []string{"hubot-robawt-abc", "monalisa-spoonknife-c4f3"},
			wantStdout:  "",
		},
		{
			name: "deletion failed",
			opts: deleteOptions{
				deleteAll: true,
			},
			codespaces: []*api.Codespace{
				{
					Name: "monalisa-spoonknife-123",
				},
				{
					Name: "hubot-robawt-abc",
				},
			},
			deleteErr:   errors.New("aborted by test"),
			wantErr:     true,
			wantDeleted: []string{"hubot-robawt-abc", "monalisa-spoonknife-123"},
			wantStderr: heredoc.Doc(`
				error deleting codespace "hubot-robawt-abc": aborted by test
				error deleting codespace "monalisa-spoonknife-123": aborted by test
			`),
		},
		{
			name: "with confirm",
			opts: deleteOptions{
				isInteractive: true,
				deleteAll:     true,
				skipConfirm:   false,
			},
			codespaces: []*api.Codespace{
				{
					Name: "monalisa-spoonknife-123",
					GitStatus: api.CodespaceGitStatus{
						HasUnpushedChanges: true,
					},
				},
				{
					Name: "hubot-robawt-abc",
					GitStatus: api.CodespaceGitStatus{
						HasUncommitedChanges: true,
					},
				},
				{
					Name: "monalisa-spoonknife-c4f3",
					GitStatus: api.CodespaceGitStatus{
						HasUnpushedChanges:   false,
						HasUncommitedChanges: false,
					},
				},
			},
			confirms: map[string]bool{
				"Codespace monalisa-spoonknife-123 has unsaved changes. OK to delete?": false,
				"Codespace hubot-robawt-abc has unsaved changes. OK to delete?":        true,
			},
			wantDeleted: []string{"hubot-robawt-abc", "monalisa-spoonknife-c4f3"},
			wantStdout:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiMock := &apiClientMock{
				GetUserFunc: func(_ context.Context) (*api.User, error) {
					return user, nil
				},
				DeleteCodespaceFunc: func(_ context.Context, name string) error {
					if tt.deleteErr != nil {
						return tt.deleteErr
					}
					return nil
				},
			}
			if tt.opts.codespaceName == "" {
				apiMock.ListCodespacesFunc = func(_ context.Context, num int) ([]*api.Codespace, error) {
					return tt.codespaces, nil
				}
			} else {
				apiMock.GetCodespaceFunc = func(_ context.Context, name string, includeConnection bool) (*api.Codespace, error) {
					return tt.codespaces[0], nil
				}
			}
			opts := tt.opts
			opts.now = func() time.Time { return now }
			opts.prompter = &prompterMock{
				ConfirmFunc: func(msg string) (bool, error) {
					res, found := tt.confirms[msg]
					if !found {
						return false, fmt.Errorf("unexpected prompt %q", msg)
					}
					return res, nil
				},
			}

			io, _, stdout, stderr := iostreams.Test()
			io.SetStdinTTY(true)
			io.SetStdoutTTY(true)
			app := NewApp(io, nil, apiMock)
			err := app.Delete(context.Background(), opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("delete() error = %v, wantErr %v", err, tt.wantErr)
			}
			var gotDeleted []string
			for _, delArgs := range apiMock.DeleteCodespaceCalls() {
				gotDeleted = append(gotDeleted, delArgs.Name)
			}
			sort.Strings(gotDeleted)
			if !sliceEquals(gotDeleted, tt.wantDeleted) {
				t.Errorf("deleted %q, want %q", gotDeleted, tt.wantDeleted)
			}
			if out := stdout.String(); out != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", out, tt.wantStdout)
			}
			if out := sortLines(stderr.String()); out != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", out, tt.wantStderr)
			}
		})
	}
}

func sliceEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortLines(s string) string {
	trailing := ""
	if strings.HasSuffix(s, "\n") {
		s = strings.TrimSuffix(s, "\n")
		trailing = "\n"
	}
	lines := strings.Split(s, "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n") + trailing
}
