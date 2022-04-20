package refresh

import (
	"errors"
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/internal/authflow"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/pkg/cmd/auth/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/spf13/cobra"
)

type RefreshOptions struct {
	IO     *iostreams.IOStreams
	Config func() (config.Config, error)

	MainExecutable string

	Hostname string
	Scopes   []string
	AuthFlow func(config.Config, *iostreams.IOStreams, string, []string) error

	Interactive bool
}

func NewCmdRefresh(f *cmdutil.Factory, runF func(*RefreshOptions) error) *cobra.Command {
	opts := &RefreshOptions{
		IO:     f.IOStreams,
		Config: f.Config,
		AuthFlow: func(cfg config.Config, io *iostreams.IOStreams, hostname string, scopes []string) error {
			_, err := authflow.AuthFlowWithConfig(cfg, io, hostname, "", scopes)
			return err
		},
		MainExecutable: f.Executable,
	}

	cmd := &cobra.Command{
		Use:   "refresh",
		Args:  cobra.ExactArgs(0),
		Short: "Refresh stored authentication credentials",
		Long: heredoc.Doc(`Expand or fix the permission scopes for stored credentials

			The --scopes flag accepts a comma separated list of scopes you want your gh credentials to have. If
			absent, this command ensures that gh has access to a minimum set of scopes.
		`),
		Example: heredoc.Doc(`
			$ gh auth refresh --scopes write:org,read:public_key
			# => open a browser to add write:org and read:public_key scopes for use with gh api

			$ gh auth refresh
			# => open a browser to ensure your authentication credentials have the correct minimum scopes
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Interactive = opts.IO.CanPrompt()

			if !opts.Interactive && opts.Hostname == "" {
				return &cmdutil.FlagError{Err: errors.New("--hostname required when not running interactively")}
			}

			if runF != nil {
				return runF(opts)
			}
			return refreshRun(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Hostname, "hostname", "h", "", "The GitHub host to use for authentication")
	cmd.Flags().StringSliceVarP(&opts.Scopes, "scopes", "s", nil, "Additional authentication scopes for gh to have")

	return cmd
}

func refreshRun(opts *RefreshOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	candidates, err := cfg.Hosts()
	if err != nil {
		return fmt.Errorf("not logged in to any hosts. Use 'gh auth login' to authenticate with a host")
	}

	hostname := opts.Hostname
	if hostname == "" {
		if len(candidates) == 1 {
			hostname = candidates[0]
		} else {
			err := prompt.SurveyAskOne(&survey.Select{
				Message: "What account do you want to refresh auth for?",
				Options: candidates,
			}, &hostname)

			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}
		}
	} else {
		var found bool
		for _, c := range candidates {
			if c == hostname {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("not logged in to %s. use 'gh auth login' to authenticate with this host", hostname)
		}
	}

	if err := cfg.CheckWriteable(hostname, "oauth_token"); err != nil {
		var roErr *config.ReadOnlyEnvError
		if errors.As(err, &roErr) {
			fmt.Fprintf(opts.IO.ErrOut, "The value of the %s environment variable is being used for authentication.\n", roErr.Variable)
			fmt.Fprint(opts.IO.ErrOut, "To refresh credentials stored in GitHub CLI, first clear the value from the environment.\n")
			return cmdutil.SilentError
		}
		return err
	}

	var additionalScopes []string

	credentialFlow := &shared.GitCredentialFlow{}
	gitProtocol, _ := cfg.Get(hostname, "git_protocol")
	if opts.Interactive && gitProtocol == "https" {
		if err := credentialFlow.Prompt(hostname); err != nil {
			return err
		}
		additionalScopes = append(additionalScopes, credentialFlow.Scopes()...)
	}

	if err := opts.AuthFlow(cfg, opts.IO, hostname, append(opts.Scopes, additionalScopes...)); err != nil {
		return err
	}

	if credentialFlow.ShouldSetup() {
		username, _ := cfg.Get(hostname, "user")
		password, _ := cfg.Get(hostname, "oauth_token")
		if err := credentialFlow.Setup(hostname, username, password); err != nil {
			return err
		}
	}

	return nil
}
