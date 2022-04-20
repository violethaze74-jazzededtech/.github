package remove

import (
	"fmt"
	"net/http"

	"github.com/cli/cli/api"
	"github.com/cli/cli/internal/ghinstance"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/spf13/cobra"
)

type RemoveOptions struct {
	HttpClient func() (*http.Client, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)

	SecretName string
	OrgName    string
}

func NewCmdRemove(f *cmdutil.Factory, runF func(*RemoveOptions) error) *cobra.Command {
	opts := &RemoveOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
	}

	cmd := &cobra.Command{
		Use:   "remove <secret-name>",
		Short: "Remove an organization or repository secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			opts.SecretName = args[0]

			if runF != nil {
				return runF(opts)
			}

			return removeRun(opts)
		},
	}
	cmd.Flags().StringVarP(&opts.OrgName, "org", "o", "", "List secrets for an organization")

	return cmd
}

func removeRun(opts *RemoveOptions) error {
	c, err := opts.HttpClient()
	if err != nil {
		return fmt.Errorf("could not create http client: %w", err)
	}
	client := api.NewClientFromHTTP(c)

	orgName := opts.OrgName

	var baseRepo ghrepo.Interface
	if orgName == "" {
		baseRepo, err = opts.BaseRepo()
		if err != nil {
			return fmt.Errorf("could not determine base repo: %w", err)
		}
	}

	var path string
	if orgName == "" {
		path = fmt.Sprintf("repos/%s/actions/secrets/%s", ghrepo.FullName(baseRepo), opts.SecretName)
	} else {
		path = fmt.Sprintf("orgs/%s/actions/secrets/%s", orgName, opts.SecretName)
	}

	host := ghinstance.OverridableDefault()
	err = client.REST(host, "DELETE", path, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to delete secret %s: %w", opts.SecretName, err)
	}

	if opts.IO.IsStdoutTTY() {
		cs := opts.IO.ColorScheme()
		if orgName == "" {
			fmt.Fprintf(opts.IO.Out,
				"%s Removed secret %s from %s\n", cs.SuccessIcon(), opts.SecretName, ghrepo.FullName(baseRepo))
		} else {
			fmt.Fprintf(opts.IO.Out,
				"%s Removed secret %s from %s\n", cs.SuccessIcon(), opts.SecretName, orgName)
		}
	}

	return nil
}
