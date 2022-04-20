package deleteasset

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/httpmock"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_NewCmdDeleteAsset(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		isTTY   bool
		want    DeleteAssetOptions
		wantErr string
	}{
		{
			name:  "tag and asset arguments",
			args:  "v1.2.3 test-asset",
			isTTY: true,
			want: DeleteAssetOptions{
				TagName:     "v1.2.3",
				SkipConfirm: false,
				AssetName:   "test-asset",
			},
		},
		{
			name:  "skip confirm",
			args:  "v1.2.3 test-asset -y",
			isTTY: true,
			want: DeleteAssetOptions{
				TagName:     "v1.2.3",
				SkipConfirm: true,
				AssetName:   "test-asset",
			},
		},
		{
			name:    "no arguments",
			args:    "",
			isTTY:   true,
			wantErr: "accepts 2 arg(s), received 0",
		},
		{
			name:    "one arguments",
			args:    "v1.2.3",
			isTTY:   true,
			wantErr: "accepts 2 arg(s), received 1",
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

			var opts *DeleteAssetOptions
			cmd := NewCmdDeleteAsset(f, func(o *DeleteAssetOptions) error {
				opts = o
				return nil
			})

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

			assert.Equal(t, tt.want.TagName, opts.TagName)
			assert.Equal(t, tt.want.SkipConfirm, opts.SkipConfirm)
			assert.Equal(t, tt.want.AssetName, opts.AssetName)
		})
	}
}

func Test_deleteAssetRun(t *testing.T) {
	tests := []struct {
		name       string
		isTTY      bool
		opts       DeleteAssetOptions
		wantErr    string
		wantStdout string
		wantStderr string
	}{
		{
			name:  "skipping confirmation",
			isTTY: true,
			opts: DeleteAssetOptions{
				TagName:     "v1.2.3",
				SkipConfirm: true,
				AssetName:   "test-asset",
			},
			wantStdout: ``,
			wantStderr: "✓ Deleted asset test-asset from release v1.2.3\n",
		},
		{
			name:  "non-interactive",
			isTTY: false,
			opts: DeleteAssetOptions{
				TagName:     "v1.2.3",
				SkipConfirm: false,
				AssetName:   "test-asset",
			},
			wantStdout: ``,
			wantStderr: ``,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, stdout, stderr := iostreams.Test()
			io.SetStdoutTTY(tt.isTTY)
			io.SetStdinTTY(tt.isTTY)
			io.SetStderrTTY(tt.isTTY)

			fakeHTTP := &httpmock.Registry{}
			fakeHTTP.Register(httpmock.REST("GET", "repos/OWNER/REPO/releases/tags/v1.2.3"), httpmock.StringResponse(`{
				"tag_name": "v1.2.3",
				"draft": false,
				"url": "https://api.github.com/repos/OWNER/REPO/releases/23456",
				"assets": [
          {
            "url": "https://api.github.com/repos/OWNER/REPO/releases/assets/1",
            "id": 1,
            "name": "test-asset"
          }
				]
			}`))
			fakeHTTP.Register(httpmock.REST("DELETE", "repos/OWNER/REPO/releases/assets/1"), httpmock.StatusStringResponse(204, ""))

			tt.opts.IO = io
			tt.opts.HttpClient = func() (*http.Client, error) {
				return &http.Client{Transport: fakeHTTP}, nil
			}
			tt.opts.BaseRepo = func() (ghrepo.Interface, error) {
				return ghrepo.FromFullName("OWNER/REPO")
			}

			err := deleteAssetRun(&tt.opts)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantStdout, stdout.String())
			assert.Equal(t, tt.wantStderr, stderr.String())
		})
	}
}
