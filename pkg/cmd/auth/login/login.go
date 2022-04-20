package login

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/api"
	"github.com/cli/cli/internal/authflow"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghinstance"
	"github.com/cli/cli/pkg/cmd/auth/client"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/spf13/cobra"
)

type LoginOptions struct {
	IO     *iostreams.IOStreams
	Config func() (config.Config, error)

	Interactive bool

	Hostname string
	Scopes   []string
	Token    string
	Web      bool
}

func NewCmdLogin(f *cmdutil.Factory, runF func(*LoginOptions) error) *cobra.Command {
	opts := &LoginOptions{
		IO:     f.IOStreams,
		Config: f.Config,
	}

	var tokenStdin bool

	cmd := &cobra.Command{
		Use:   "login",
		Args:  cobra.ExactArgs(0),
		Short: "Authenticate with a GitHub host",
		Long: heredoc.Docf(`
			Authenticate with a GitHub host.

			The default authentication mode is a web-based browser flow.

			Alternatively, pass in a token on standard input by using %[1]s--with-token%[1]s.
			The minimum required scopes for the token are: "repo", "read:org".

			The --scopes flag accepts a comma separated list of scopes you want your gh credentials to have. If
			absent, this command ensures that gh has access to a minimum set of scopes.
		`, "`"),
		Example: heredoc.Doc(`
			# start interactive setup
			$ gh auth login

			# authenticate against github.com by reading the token from a file
			$ gh auth login --with-token < mytoken.txt

			# authenticate with a specific GitHub Enterprise Server instance
			$ gh auth login --hostname enterprise.internal
		`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !opts.IO.CanPrompt() && !(tokenStdin || opts.Web) {
				return &cmdutil.FlagError{Err: errors.New("--web or --with-token required when not running interactively")}
			}

			if tokenStdin && opts.Web {
				return &cmdutil.FlagError{Err: errors.New("specify only one of --web or --with-token")}
			}

			if tokenStdin {
				defer opts.IO.In.Close()
				token, err := ioutil.ReadAll(opts.IO.In)
				if err != nil {
					return fmt.Errorf("failed to read token from STDIN: %w", err)
				}
				opts.Token = strings.TrimSpace(string(token))
			}

			if opts.IO.CanPrompt() && opts.Token == "" && !opts.Web {
				opts.Interactive = true
			}

			if cmd.Flags().Changed("hostname") {
				if err := ghinstance.HostnameValidator(opts.Hostname); err != nil {
					return &cmdutil.FlagError{Err: fmt.Errorf("error parsing --hostname: %w", err)}
				}
			}

			if !opts.Interactive {
				if opts.Hostname == "" {
					opts.Hostname = ghinstance.Default()
				}
			}

			if runF != nil {
				return runF(opts)
			}

			return loginRun(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Hostname, "hostname", "h", "", "The hostname of the GitHub instance to authenticate with")
	cmd.Flags().StringSliceVarP(&opts.Scopes, "scopes", "s", nil, "Additional authentication scopes for gh to have")
	cmd.Flags().BoolVar(&tokenStdin, "with-token", false, "Read token from standard input")
	cmd.Flags().BoolVarP(&opts.Web, "web", "w", false, "Open a browser to authenticate")

	return cmd
}

func loginRun(opts *LoginOptions) error {
	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	if opts.Token != "" {
		// I chose to not error on existing host here; my thinking is that for --with-token the user
		// probably doesn't care if a token is overwritten since they have a token in hand they
		// explicitly want to use.
		if opts.Hostname == "" {
			return errors.New("empty hostname would leak oauth_token")
		}

		err := cfg.Set(opts.Hostname, "oauth_token", opts.Token)
		if err != nil {
			return err
		}

		err = client.ValidateHostCfg(opts.Hostname, cfg)
		if err != nil {
			return err
		}

		return cfg.Write()
	}

	// TODO consider explicitly telling survey what io to use since it's implicit right now

	hostname := opts.Hostname

	if hostname == "" {
		var hostType int
		err := prompt.SurveyAskOne(&survey.Select{
			Message: "What account do you want to log into?",
			Options: []string{
				"GitHub.com",
				"GitHub Enterprise Server",
			},
		}, &hostType)

		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}

		isEnterprise := hostType == 1

		hostname = ghinstance.Default()
		if isEnterprise {
			err := prompt.SurveyAskOne(&survey.Input{
				Message: "GHE hostname:",
			}, &hostname, survey.WithValidator(ghinstance.HostnameValidator))
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}
		}
	}

	fmt.Fprintf(opts.IO.ErrOut, "- Logging into %s\n", hostname)

	existingToken, _ := cfg.Get(hostname, "oauth_token")

	if existingToken != "" && opts.Interactive {
		err := client.ValidateHostCfg(hostname, cfg)
		if err == nil {
			apiClient, err := client.ClientFromCfg(hostname, cfg)
			if err != nil {
				return err
			}

			username, err := api.CurrentLoginName(apiClient, hostname)
			if err != nil {
				return fmt.Errorf("error using api: %w", err)
			}
			var keepGoing bool
			err = prompt.SurveyAskOne(&survey.Confirm{
				Message: fmt.Sprintf(
					"You're already logged into %s as %s. Do you want to re-authenticate?",
					hostname,
					username),
				Default: false,
			}, &keepGoing)
			if err != nil {
				return fmt.Errorf("could not prompt: %w", err)
			}

			if !keepGoing {
				return nil
			}
		}
	}

	if err := cfg.CheckWriteable(hostname, "oauth_token"); err != nil {
		return err
	}

	var authMode int
	if opts.Web {
		authMode = 0
	} else {
		err = prompt.SurveyAskOne(&survey.Select{
			Message: "How would you like to authenticate?",
			Options: []string{
				"Login with a web browser",
				"Paste an authentication token",
			},
		}, &authMode)
		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}
	}

	if authMode == 0 {
		_, err := authflow.AuthFlowWithConfig(cfg, opts.IO, hostname, "", opts.Scopes)
		if err != nil {
			return fmt.Errorf("failed to authenticate via web browser: %w", err)
		}
	} else {
		fmt.Fprintln(opts.IO.ErrOut)
		fmt.Fprintln(opts.IO.ErrOut, heredoc.Doc(getAccessTokenTip(hostname)))
		var token string
		err := prompt.SurveyAskOne(&survey.Password{
			Message: "Paste your authentication token:",
		}, &token, survey.WithValidator(survey.Required))
		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}

		if hostname == "" {
			return errors.New("empty hostname would leak oauth_token")
		}

		err = cfg.Set(hostname, "oauth_token", token)
		if err != nil {
			return err
		}

		err = client.ValidateHostCfg(hostname, cfg)
		if err != nil {
			return err
		}
	}

	cs := opts.IO.ColorScheme()

	gitProtocol := "https"
	if opts.Interactive {
		err = prompt.SurveyAskOne(&survey.Select{
			Message: "Choose default git protocol",
			Options: []string{
				"HTTPS",
				"SSH",
			},
		}, &gitProtocol)
		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}

		gitProtocol = strings.ToLower(gitProtocol)

		fmt.Fprintf(opts.IO.ErrOut, "- gh config set -h %s git_protocol %s\n", hostname, gitProtocol)
		err = cfg.Set(hostname, "git_protocol", gitProtocol)
		if err != nil {
			return err
		}

		fmt.Fprintf(opts.IO.ErrOut, "%s Configured git protocol\n", cs.SuccessIcon())
	}

	apiClient, err := client.ClientFromCfg(hostname, cfg)
	if err != nil {
		return err
	}

	username, err := api.CurrentLoginName(apiClient, hostname)
	if err != nil {
		return fmt.Errorf("error using api: %w", err)
	}

	err = cfg.Set(hostname, "user", username)
	if err != nil {
		return err
	}

	err = cfg.Write()
	if err != nil {
		return err
	}

	fmt.Fprintf(opts.IO.ErrOut, "%s Logged in as %s\n", cs.SuccessIcon(), cs.Bold(username))

	return nil
}

func getAccessTokenTip(hostname string) string {
	ghHostname := hostname
	if ghHostname == "" {
		ghHostname = ghinstance.OverridableDefault()
	}
	return fmt.Sprintf(`
	Tip: you can generate a Personal Access Token here https://%s/settings/tokens
	The minimum required scopes are 'repo' and 'read:org'.`, ghHostname)
}
