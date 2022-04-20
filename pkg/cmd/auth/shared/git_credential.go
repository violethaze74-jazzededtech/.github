package shared

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/v2/git"
	"github.com/cli/cli/v2/internal/ghinstance"
	"github.com/cli/cli/v2/internal/run"
	"github.com/cli/cli/v2/pkg/prompt"
	"github.com/google/shlex"
)

type GitCredentialFlow struct {
	Executable string

	shouldSetup bool
	helper      string
	scopes      []string
}

func (flow *GitCredentialFlow) Prompt(hostname string) error {
	var gitErr error
	flow.helper, gitErr = gitCredentialHelper(hostname)
	if isOurCredentialHelper(flow.helper) {
		flow.scopes = append(flow.scopes, "workflow")
		return nil
	}

	err := prompt.SurveyAskOne(&survey.Confirm{
		Message: "Authenticate Git with your GitHub credentials?",
		Default: true,
	}, &flow.shouldSetup)
	if err != nil {
		return fmt.Errorf("could not prompt: %w", err)
	}
	if flow.shouldSetup {
		if isGitMissing(gitErr) {
			return gitErr
		}
		flow.scopes = append(flow.scopes, "workflow")
	}

	return nil
}

func (flow *GitCredentialFlow) Scopes() []string {
	return flow.scopes
}

func (flow *GitCredentialFlow) ShouldSetup() bool {
	return flow.shouldSetup
}

func (flow *GitCredentialFlow) Setup(hostname, username, authToken string) error {
	return flow.gitCredentialSetup(hostname, username, authToken)
}

func (flow *GitCredentialFlow) gitCredentialSetup(hostname, username, password string) error {
	if flow.helper == "" {
		credHelperKeys := []string{
			gitCredentialHelperKey(hostname),
		}

		gistHost := strings.TrimSuffix(ghinstance.GistHost(hostname), "/")
		if strings.HasPrefix(gistHost, "gist.") {
			credHelperKeys = append(credHelperKeys, gitCredentialHelperKey(gistHost))
		}

		var configErr error

		for _, credHelperKey := range credHelperKeys {
			if configErr != nil {
				break
			}
			// first use a blank value to indicate to git we want to sever the chain of credential helpers
			preConfigureCmd, err := git.GitCommand("config", "--global", "--replace-all", credHelperKey, "")
			if err != nil {
				configErr = err
				break
			}
			if err = run.PrepareCmd(preConfigureCmd).Run(); err != nil {
				configErr = err
				break
			}

			// second configure the actual helper for this host
			configureCmd, err := git.GitCommand(
				"config", "--global", "--add",
				credHelperKey,
				fmt.Sprintf("!%s auth git-credential", shellQuote(flow.Executable)),
			)
			if err != nil {
				configErr = err
			} else {
				configErr = run.PrepareCmd(configureCmd).Run()
			}
		}

		return configErr
	}

	// clear previous cached credentials
	rejectCmd, err := git.GitCommand("credential", "reject")
	if err != nil {
		return err
	}

	rejectCmd.Stdin = bytes.NewBufferString(heredoc.Docf(`
		protocol=https
		host=%s
	`, hostname))

	err = run.PrepareCmd(rejectCmd).Run()
	if err != nil {
		return err
	}

	approveCmd, err := git.GitCommand("credential", "approve")
	if err != nil {
		return err
	}

	approveCmd.Stdin = bytes.NewBufferString(heredoc.Docf(`
		protocol=https
		host=%s
		username=%s
		password=%s
	`, hostname, username, password))

	err = run.PrepareCmd(approveCmd).Run()
	if err != nil {
		return err
	}

	return nil
}

func gitCredentialHelperKey(hostname string) string {
	host := strings.TrimSuffix(ghinstance.HostPrefix(hostname), "/")
	return fmt.Sprintf("credential.%s.helper", host)
}

func gitCredentialHelper(hostname string) (helper string, err error) {
	helper, err = git.Config(gitCredentialHelperKey(hostname))
	if helper != "" {
		return
	}
	helper, err = git.Config("credential.helper")
	return
}

func isOurCredentialHelper(cmd string) bool {
	if !strings.HasPrefix(cmd, "!") {
		return false
	}

	args, err := shlex.Split(cmd[1:])
	if err != nil || len(args) == 0 {
		return false
	}

	return strings.TrimSuffix(filepath.Base(args[0]), ".exe") == "gh"
}

func isGitMissing(err error) bool {
	if err == nil {
		return false
	}
	var errNotInstalled *git.NotInstalled
	return errors.As(err, &errNotInstalled)
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " $") {
		return "'" + s + "'"
	}
	return s
}
