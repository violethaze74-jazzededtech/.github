package merge

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/api"
	"github.com/cli/cli/context"
	"github.com/cli/cli/git"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/pr/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/cli/cli/pkg/surveyext"
	"github.com/spf13/cobra"
)

type editor interface {
	Edit(string, string) (string, error)
}

type MergeOptions struct {
	HttpClient func() (*http.Client, error)
	Config     func() (config.Config, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)
	Remotes    func() (context.Remotes, error)
	Branch     func() (string, error)

	SelectorArg  string
	DeleteBranch bool
	MergeMethod  PullRequestMergeMethod

	AutoMergeEnable  bool
	AutoMergeDisable bool

	Body    string
	BodySet bool
	Editor  editor

	IsDeleteBranchIndicated bool
	CanDeleteLocalBranch    bool
	InteractiveMode         bool
}

func NewCmdMerge(f *cmdutil.Factory, runF func(*MergeOptions) error) *cobra.Command {
	opts := &MergeOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Config:     f.Config,
		Remotes:    f.Remotes,
		Branch:     f.Branch,
	}

	var (
		flagMerge  bool
		flagSquash bool
		flagRebase bool
	)

	cmd := &cobra.Command{
		Use:   "merge [<number> | <url> | <branch>]",
		Short: "Merge a pull request",
		Long: heredoc.Doc(`
			Merge a pull request on GitHub.
    	`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			if repoOverride, _ := cmd.Flags().GetString("repo"); repoOverride != "" && len(args) == 0 {
				return &cmdutil.FlagError{Err: errors.New("argument required when using the --repo flag")}
			}

			if len(args) > 0 {
				opts.SelectorArg = args[0]
			}

			methodFlags := 0
			if flagMerge {
				opts.MergeMethod = PullRequestMergeMethodMerge
				methodFlags++
			}
			if flagRebase {
				opts.MergeMethod = PullRequestMergeMethodRebase
				methodFlags++
			}
			if flagSquash {
				opts.MergeMethod = PullRequestMergeMethodSquash
				methodFlags++
			}
			if methodFlags == 0 {
				if !opts.IO.CanPrompt() {
					return &cmdutil.FlagError{Err: errors.New("--merge, --rebase, or --squash required when not running interactively")}
				}
				opts.InteractiveMode = true
			} else if methodFlags > 1 {
				return &cmdutil.FlagError{Err: errors.New("only one of --merge, --rebase, or --squash can be enabled")}
			}

			opts.IsDeleteBranchIndicated = cmd.Flags().Changed("delete-branch")
			opts.CanDeleteLocalBranch = !cmd.Flags().Changed("repo")
			opts.BodySet = cmd.Flags().Changed("body")

			opts.Editor = &userEditor{
				io:     opts.IO,
				config: opts.Config,
			}

			if runF != nil {
				return runF(opts)
			}
			return mergeRun(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.DeleteBranch, "delete-branch", "d", false, "Delete the local and remote branch after merge")
	cmd.Flags().StringVarP(&opts.Body, "body", "b", "", "Body `text` for the merge commit")
	cmd.Flags().BoolVarP(&flagMerge, "merge", "m", false, "Merge the commits with the base branch")
	cmd.Flags().BoolVarP(&flagRebase, "rebase", "r", false, "Rebase the commits onto the base branch")
	cmd.Flags().BoolVarP(&flagSquash, "squash", "s", false, "Squash the commits into one commit and merge it into the base branch")
	cmd.Flags().BoolVar(&opts.AutoMergeEnable, "auto", false, "Automatically merge only after necessary requirements are met")
	cmd.Flags().BoolVar(&opts.AutoMergeDisable, "disable-auto", false, "Disable auto-merge for this pull request")
	return cmd
}

func mergeRun(opts *MergeOptions) error {
	cs := opts.IO.ColorScheme()

	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}
	apiClient := api.NewClientFromHTTP(httpClient)

	pr, baseRepo, err := shared.PRFromArgs(apiClient, opts.BaseRepo, opts.Branch, opts.Remotes, opts.SelectorArg)
	if err != nil {
		return err
	}

	isTerminal := opts.IO.IsStdoutTTY()

	if opts.AutoMergeDisable {
		err := disableAutoMerge(httpClient, baseRepo, pr.ID)
		if err != nil {
			return err
		}
		if isTerminal {
			fmt.Fprintf(opts.IO.ErrOut, "%s Auto-merge disabled for pull request #%d\n", cs.SuccessIconWithColor(cs.Green), pr.Number)
		}
		return nil
	}

	if opts.SelectorArg == "" {
		localBranchLastCommit, err := git.LastCommit()
		if err == nil {
			if localBranchLastCommit.Sha != pr.Commits.Nodes[0].Commit.Oid {
				fmt.Fprintf(opts.IO.ErrOut,
					"%s Pull request #%d (%s) has diverged from local branch\n", cs.Yellow("!"), pr.Number, pr.Title)
			}
		}
	}

	if pr.Mergeable == "CONFLICTING" {
		fmt.Fprintf(opts.IO.ErrOut, "%s Pull request #%d (%s) has conflicts and isn't mergeable\n", cs.Red("!"), pr.Number, pr.Title)
		return cmdutil.SilentError
	}

	deleteBranch := opts.DeleteBranch
	crossRepoPR := pr.HeadRepositoryOwner.Login != baseRepo.RepoOwner()

	isPRAlreadyMerged := pr.State == "MERGED"
	if !isPRAlreadyMerged {
		payload := mergePayload{
			repo:          baseRepo,
			pullRequestID: pr.ID,
			method:        opts.MergeMethod,
			auto:          opts.AutoMergeEnable,
			commitBody:    opts.Body,
			setCommitBody: opts.BodySet,
		}

		if opts.InteractiveMode {
			r, err := api.GitHubRepo(apiClient, baseRepo)
			if err != nil {
				return err
			}
			payload.method, err = mergeMethodSurvey(r)
			if err != nil {
				return err
			}
			deleteBranch, err = deleteBranchSurvey(opts, crossRepoPR)
			if err != nil {
				return err
			}

			allowEditMsg := payload.method != PullRequestMergeMethodRebase

			action, err := confirmSurvey(allowEditMsg)
			if err != nil {
				return fmt.Errorf("unable to confirm: %w", err)
			}

			if action == shared.EditCommitMessageAction {
				if !payload.setCommitBody {
					payload.commitBody, err = getMergeText(httpClient, baseRepo, pr.ID, payload.method)
					if err != nil {
						return err
					}
				}

				payload.commitBody, err = opts.Editor.Edit("*.md", payload.commitBody)
				if err != nil {
					return err
				}
				payload.setCommitBody = true

				action, err = confirmSurvey(false)
				if err != nil {
					return fmt.Errorf("unable to confirm: %w", err)
				}
			}
			if action == shared.CancelAction {
				fmt.Fprintln(opts.IO.ErrOut, "Cancelled.")
				return cmdutil.SilentError
			}
		}

		err = mergePullRequest(httpClient, payload)
		if err != nil {
			return err
		}

		if isTerminal {
			if payload.auto {
				method := ""
				switch payload.method {
				case PullRequestMergeMethodRebase:
					method = " via rebase"
				case PullRequestMergeMethodSquash:
					method = " via squash"
				}
				fmt.Fprintf(opts.IO.ErrOut, "%s Pull request #%d will be automatically merged%s when all requirements are met\n", cs.SuccessIconWithColor(cs.Green), pr.Number, method)
			} else {
				action := "Merged"
				switch payload.method {
				case PullRequestMergeMethodRebase:
					action = "Rebased and merged"
				case PullRequestMergeMethodSquash:
					action = "Squashed and merged"
				}
				fmt.Fprintf(opts.IO.ErrOut, "%s %s pull request #%d (%s)\n", cs.SuccessIconWithColor(cs.Magenta), action, pr.Number, pr.Title)
			}
		}
	} else if !opts.IsDeleteBranchIndicated && opts.InteractiveMode && !crossRepoPR && !opts.AutoMergeEnable {
		err := prompt.SurveyAskOne(&survey.Confirm{
			Message: fmt.Sprintf("Pull request #%d was already merged. Delete the branch locally?", pr.Number),
			Default: false,
		}, &deleteBranch)
		if err != nil {
			return fmt.Errorf("could not prompt: %w", err)
		}
	} else if crossRepoPR {
		fmt.Fprintf(opts.IO.ErrOut, "%s Pull request #%d was already merged\n", cs.WarningIcon(), pr.Number)
	}

	if !deleteBranch || crossRepoPR || opts.AutoMergeEnable {
		return nil
	}

	branchSwitchString := ""

	if opts.CanDeleteLocalBranch {
		currentBranch, err := opts.Branch()
		if err != nil {
			return err
		}

		var branchToSwitchTo string
		if currentBranch == pr.HeadRefName {
			branchToSwitchTo, err = api.RepoDefaultBranch(apiClient, baseRepo)
			if err != nil {
				return err
			}
			err = git.CheckoutBranch(branchToSwitchTo)
			if err != nil {
				return err
			}
		}

		localBranchExists := git.HasLocalBranch(pr.HeadRefName)
		if localBranchExists {
			err = git.DeleteLocalBranch(pr.HeadRefName)
			if err != nil {
				err = fmt.Errorf("failed to delete local branch %s: %w", cs.Cyan(pr.HeadRefName), err)
				return err
			}
		}

		if branchToSwitchTo != "" {
			branchSwitchString = fmt.Sprintf(" and switched to branch %s", cs.Cyan(branchToSwitchTo))
		}
	}

	if !isPRAlreadyMerged {
		err = api.BranchDeleteRemote(apiClient, baseRepo, pr.HeadRefName)
		var httpErr api.HTTPError
		// The ref might have already been deleted by GitHub
		if err != nil && (!errors.As(err, &httpErr) || httpErr.StatusCode != 422) {
			err = fmt.Errorf("failed to delete remote branch %s: %w", cs.Cyan(pr.HeadRefName), err)
			return err
		}
	}

	if isTerminal {
		fmt.Fprintf(opts.IO.ErrOut, "%s Deleted branch %s%s\n", cs.SuccessIconWithColor(cs.Red), cs.Cyan(pr.HeadRefName), branchSwitchString)
	}

	return nil
}

func mergeMethodSurvey(baseRepo *api.Repository) (PullRequestMergeMethod, error) {
	type mergeOption struct {
		title  string
		method PullRequestMergeMethod
	}

	var mergeOpts []mergeOption
	if baseRepo.MergeCommitAllowed {
		opt := mergeOption{title: "Create a merge commit", method: PullRequestMergeMethodMerge}
		mergeOpts = append(mergeOpts, opt)
	}
	if baseRepo.RebaseMergeAllowed {
		opt := mergeOption{title: "Rebase and merge", method: PullRequestMergeMethodRebase}
		mergeOpts = append(mergeOpts, opt)
	}
	if baseRepo.SquashMergeAllowed {
		opt := mergeOption{title: "Squash and merge", method: PullRequestMergeMethodSquash}
		mergeOpts = append(mergeOpts, opt)
	}

	var surveyOpts []string
	for _, v := range mergeOpts {
		surveyOpts = append(surveyOpts, v.title)
	}

	mergeQuestion := &survey.Select{
		Message: "What merge method would you like to use?",
		Options: surveyOpts,
	}

	var result int
	err := prompt.SurveyAskOne(mergeQuestion, &result)
	return mergeOpts[result].method, err
}

func deleteBranchSurvey(opts *MergeOptions, crossRepoPR bool) (bool, error) {
	if !crossRepoPR && !opts.IsDeleteBranchIndicated {
		var message string
		if opts.CanDeleteLocalBranch {
			message = "Delete the branch locally and on GitHub?"
		} else {
			message = "Delete the branch on GitHub?"
		}

		var result bool
		submit := &survey.Confirm{
			Message: message,
			Default: false,
		}
		err := prompt.SurveyAskOne(submit, &result)
		return result, err
	}

	return opts.DeleteBranch, nil
}

func confirmSurvey(allowEditMsg bool) (shared.Action, error) {
	const (
		submitLabel        = "Submit"
		editCommitMsgLabel = "Edit commit message"
		cancelLabel        = "Cancel"
	)

	options := []string{submitLabel}
	if allowEditMsg {
		options = append(options, editCommitMsgLabel)
	}
	options = append(options, cancelLabel)

	var result string
	submit := &survey.Select{
		Message: "What's next?",
		Options: options,
	}
	err := prompt.SurveyAskOne(submit, &result)
	if err != nil {
		return shared.CancelAction, fmt.Errorf("could not prompt: %w", err)
	}

	switch result {
	case submitLabel:
		return shared.SubmitAction, nil
	case editCommitMsgLabel:
		return shared.EditCommitMessageAction, nil
	default:
		return shared.CancelAction, nil
	}
}

type userEditor struct {
	io     *iostreams.IOStreams
	config func() (config.Config, error)
}

func (e *userEditor) Edit(filename, startingText string) (string, error) {
	editorCommand, err := cmdutil.DetermineEditor(e.config)
	if err != nil {
		return "", err
	}

	return surveyext.Edit(editorCommand, filename, startingText, e.io.In, e.io.Out, e.io.ErrOut, nil)
}
