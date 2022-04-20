package create

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/api"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	prShared "github.com/cli/cli/pkg/cmd/pr/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/utils"
	"github.com/spf13/cobra"
)

type CreateOptions struct {
	HttpClient func() (*http.Client, error)
	Config     func() (config.Config, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)

	RootDirOverride string

	RepoOverride string
	WebMode      bool

	Title       string
	Body        string
	Interactive bool

	Assignees []string
	Labels    []string
	Projects  []string
	Milestone string
}

func NewCmdCreate(f *cmdutil.Factory, runF func(*CreateOptions) error) *cobra.Command {
	opts := &CreateOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Config:     f.Config,
	}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new issue",
		Example: heredoc.Doc(`
			$ gh issue create --title "I found a bug" --body "Nothing works"
			$ gh issue create --label "bug,help wanted"
			$ gh issue create --label bug --label "help wanted"
			$ gh issue create --assignee monalisa,hubot
			$ gh issue create --project "Roadmap"
		`),
		Args: cmdutil.NoArgsQuoteReminder,
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			titleProvided := cmd.Flags().Changed("title")
			bodyProvided := cmd.Flags().Changed("body")
			opts.RepoOverride, _ = cmd.Flags().GetString("repo")

			opts.Interactive = !(titleProvided && bodyProvided)

			if opts.Interactive && !opts.IO.CanPrompt() {
				return &cmdutil.FlagError{Err: errors.New("must provide --title and --body when not running interactively")}
			}

			if runF != nil {
				return runF(opts)
			}
			return createRun(opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Title, "title", "t", "", "Supply a title. Will prompt for one otherwise.")
	cmd.Flags().StringVarP(&opts.Body, "body", "b", "", "Supply a body. Will prompt for one otherwise.")
	cmd.Flags().BoolVarP(&opts.WebMode, "web", "w", false, "Open the browser to create an issue")
	cmd.Flags().StringSliceVarP(&opts.Assignees, "assignee", "a", nil, "Assign people by their `login`")
	cmd.Flags().StringSliceVarP(&opts.Labels, "label", "l", nil, "Add labels by `name`")
	cmd.Flags().StringSliceVarP(&opts.Projects, "project", "p", nil, "Add the issue to projects by `name`")
	cmd.Flags().StringVarP(&opts.Milestone, "milestone", "m", "", "Add the issue to a milestone by `name`")

	return cmd
}

func createRun(opts *CreateOptions) error {
	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}
	apiClient := api.NewClientFromHTTP(httpClient)

	baseRepo, err := opts.BaseRepo()
	if err != nil {
		return err
	}

	templateFiles, legacyTemplate := prShared.FindTemplates(opts.RootDirOverride, "ISSUE_TEMPLATE")

	isTerminal := opts.IO.IsStdoutTTY()

	var milestones []string
	if opts.Milestone != "" {
		milestones = []string{opts.Milestone}
	}

	tb := prShared.IssueMetadataState{
		Type:       prShared.IssueMetadata,
		Assignees:  opts.Assignees,
		Labels:     opts.Labels,
		Projects:   opts.Projects,
		Milestones: milestones,
		Title:      opts.Title,
		Body:       opts.Body,
	}

	if opts.WebMode {
		openURL := ghrepo.GenerateRepoURL(baseRepo, "issues/new")
		if opts.Title != "" || opts.Body != "" {
			openURL, err = prShared.WithPrAndIssueQueryParams(openURL, tb)
			if err != nil {
				return err
			}
		} else if len(templateFiles) > 1 {
			openURL += "/choose"
		}
		if isTerminal {
			fmt.Fprintf(opts.IO.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(openURL))
		}
		return utils.OpenInBrowser(openURL)
	}

	if isTerminal {
		fmt.Fprintf(opts.IO.ErrOut, "\nCreating issue in %s\n\n", ghrepo.FullName(baseRepo))
	}

	repo, err := api.GitHubRepo(apiClient, baseRepo)
	if err != nil {
		return err
	}
	if !repo.HasIssuesEnabled {
		return fmt.Errorf("the '%s' repository has disabled issues", ghrepo.FullName(baseRepo))
	}

	action := prShared.SubmitAction

	if opts.Interactive {
		editorCommand, err := cmdutil.DetermineEditor(opts.Config)
		if err != nil {
			return err
		}

		if tb.Title == "" {
			err = prShared.TitleSurvey(&tb)
			if err != nil {
				return err
			}
		}

		if tb.Body == "" {
			templateContent := ""

			templateContent, err = prShared.TemplateSurvey(templateFiles, legacyTemplate, tb)
			if err != nil {
				return err
			}

			err = prShared.BodySurvey(&tb, templateContent, editorCommand)
			if err != nil {
				return err
			}

			if tb.Body == "" {
				tb.Body = templateContent
			}
		}

		action, err := prShared.ConfirmSubmission(!tb.HasMetadata(), repo.ViewerCanTriage())
		if err != nil {
			return fmt.Errorf("unable to confirm: %w", err)
		}

		if action == prShared.MetadataAction {
			err = prShared.MetadataSurvey(opts.IO, apiClient, baseRepo, &tb)
			if err != nil {
				return err
			}

			action, err = prShared.ConfirmSubmission(!tb.HasMetadata(), false)
			if err != nil {
				return err
			}
		}

		if action == prShared.CancelAction {
			fmt.Fprintln(opts.IO.ErrOut, "Discarding.")

			return nil
		}
	} else {
		if tb.Title == "" {
			return fmt.Errorf("title can't be blank")
		}
	}

	if action == prShared.PreviewAction {
		openURL := ghrepo.GenerateRepoURL(baseRepo, "issues/new")
		openURL, err = prShared.WithPrAndIssueQueryParams(openURL, tb)
		if err != nil {
			return err
		}
		if isTerminal {
			fmt.Fprintf(opts.IO.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(openURL))
		}
		return utils.OpenInBrowser(openURL)
	} else if action == prShared.SubmitAction {
		params := map[string]interface{}{
			"title": tb.Title,
			"body":  tb.Body,
		}

		err = prShared.AddMetadataToIssueParams(apiClient, baseRepo, params, &tb)
		if err != nil {
			return err
		}

		newIssue, err := api.IssueCreate(apiClient, repo, params)
		if err != nil {
			return err
		}

		fmt.Fprintln(opts.IO.Out, newIssue.URL)
	} else {
		panic("Unreachable state")
	}

	return nil
}
