package view

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/api"
	"github.com/cli/cli/context"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/pr/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/markdown"
	"github.com/cli/cli/utils"
	"github.com/spf13/cobra"
)

type ViewOptions struct {
	HttpClient func() (*http.Client, error)
	Config     func() (config.Config, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)
	Remotes    func() (context.Remotes, error)
	Branch     func() (string, error)

	SelectorArg string
	BrowserMode bool
}

func NewCmdView(f *cmdutil.Factory, runF func(*ViewOptions) error) *cobra.Command {
	opts := &ViewOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Config:     f.Config,
		Remotes:    f.Remotes,
		Branch:     f.Branch,
	}

	cmd := &cobra.Command{
		Use:   "view [<number> | <url> | <branch>]",
		Short: "View a pull request",
		Long: heredoc.Doc(`
			Display the title, body, and other information about a pull request.

			Without an argument, the pull request that belongs to the current branch
			is displayed.

			With '--web', open the pull request in a web browser instead.
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

			if runF != nil {
				return runF(opts)
			}
			return viewRun(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.BrowserMode, "web", "w", false, "Open a pull request in the browser")

	return cmd
}

func viewRun(opts *ViewOptions) error {
	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}
	apiClient := api.NewClientFromHTTP(httpClient)

	pr, _, err := shared.PRFromArgs(apiClient, opts.BaseRepo, opts.Branch, opts.Remotes, opts.SelectorArg)
	if err != nil {
		return err
	}

	openURL := pr.URL
	connectedToTerminal := opts.IO.IsStdoutTTY() && opts.IO.IsStderrTTY()

	if opts.BrowserMode {
		if connectedToTerminal {
			fmt.Fprintf(opts.IO.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(openURL))
		}
		return utils.OpenInBrowser(openURL)
	}

	opts.IO.DetectTerminalTheme()

	err = opts.IO.StartPager()
	if err != nil {
		return err
	}
	defer opts.IO.StopPager()

	if connectedToTerminal {
		return printHumanPrPreview(opts.IO, pr)
	}
	return printRawPrPreview(opts.IO.Out, pr)
}

func printRawPrPreview(out io.Writer, pr *api.PullRequest) error {
	reviewers := prReviewerList(*pr)
	assignees := prAssigneeList(*pr)
	labels := prLabelList(*pr)
	projects := prProjectList(*pr)

	fmt.Fprintf(out, "title:\t%s\n", pr.Title)
	fmt.Fprintf(out, "state:\t%s\n", prStateWithDraft(pr))
	fmt.Fprintf(out, "author:\t%s\n", pr.Author.Login)
	fmt.Fprintf(out, "labels:\t%s\n", labels)
	fmt.Fprintf(out, "assignees:\t%s\n", assignees)
	fmt.Fprintf(out, "reviewers:\t%s\n", reviewers)
	fmt.Fprintf(out, "projects:\t%s\n", projects)
	fmt.Fprintf(out, "milestone:\t%s\n", pr.Milestone.Title)
	fmt.Fprintf(out, "number:\t%d\n", pr.Number)
	fmt.Fprintf(out, "url:\t%s\n", pr.URL)

	fmt.Fprintln(out, "--")
	fmt.Fprintln(out, pr.Body)

	return nil
}

func printHumanPrPreview(io *iostreams.IOStreams, pr *api.PullRequest) error {
	out := io.Out

	// Header (Title and State)
	fmt.Fprintln(out, utils.Bold(pr.Title))
	fmt.Fprintf(out, "%s", shared.StateTitleWithColor(*pr))
	fmt.Fprintln(out, utils.Gray(fmt.Sprintf(
		" • %s wants to merge %s into %s from %s",
		pr.Author.Login,
		utils.Pluralize(pr.Commits.TotalCount, "commit"),
		pr.BaseRefName,
		pr.HeadRefName,
	)))
	fmt.Fprintln(out)

	// Metadata
	if reviewers := prReviewerList(*pr); reviewers != "" {
		fmt.Fprint(out, utils.Bold("Reviewers: "))
		fmt.Fprintln(out, reviewers)
	}
	if assignees := prAssigneeList(*pr); assignees != "" {
		fmt.Fprint(out, utils.Bold("Assignees: "))
		fmt.Fprintln(out, assignees)
	}
	if labels := prLabelList(*pr); labels != "" {
		fmt.Fprint(out, utils.Bold("Labels: "))
		fmt.Fprintln(out, labels)
	}
	if projects := prProjectList(*pr); projects != "" {
		fmt.Fprint(out, utils.Bold("Projects: "))
		fmt.Fprintln(out, projects)
	}
	if pr.Milestone.Title != "" {
		fmt.Fprint(out, utils.Bold("Milestone: "))
		fmt.Fprintln(out, pr.Milestone.Title)
	}

	// Body
	if pr.Body != "" {
		fmt.Fprintln(out)
		style := markdown.GetStyle(io.TerminalTheme())
		md, err := markdown.Render(pr.Body, style)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, md)
	}
	fmt.Fprintln(out)

	// Footer
	fmt.Fprintf(out, utils.Gray("View this pull request on GitHub: %s\n"), pr.URL)
	return nil
}

const (
	requestedReviewState        = "REQUESTED" // This is our own state for review request
	approvedReviewState         = "APPROVED"
	changesRequestedReviewState = "CHANGES_REQUESTED"
	commentedReviewState        = "COMMENTED"
	dismissedReviewState        = "DISMISSED"
	pendingReviewState          = "PENDING"
)

type reviewerState struct {
	Name  string
	State string
}

// colorFuncForReviewerState returns a color function for a reviewer state
func colorFuncForReviewerState(state string) func(string) string {
	switch state {
	case requestedReviewState:
		return utils.Yellow
	case approvedReviewState:
		return utils.Green
	case changesRequestedReviewState:
		return utils.Red
	case commentedReviewState:
		return func(str string) string { return str } // Do nothing
	default:
		return nil
	}
}

// formattedReviewerState formats a reviewerState with state color
func formattedReviewerState(reviewer *reviewerState) string {
	state := reviewer.State
	if state == dismissedReviewState {
		// Show "DISMISSED" review as "COMMENTED", since "dimissed" only makes
		// sense when displayed in an events timeline but not in the final tally.
		state = commentedReviewState
	}
	stateColorFunc := colorFuncForReviewerState(state)
	return fmt.Sprintf("%s (%s)", reviewer.Name, stateColorFunc(strings.ReplaceAll(strings.Title(strings.ToLower(state)), "_", " ")))
}

// prReviewerList generates a reviewer list with their last state
func prReviewerList(pr api.PullRequest) string {
	reviewerStates := parseReviewers(pr)
	reviewers := make([]string, 0, len(reviewerStates))

	sortReviewerStates(reviewerStates)

	for _, reviewer := range reviewerStates {
		reviewers = append(reviewers, formattedReviewerState(reviewer))
	}

	reviewerList := strings.Join(reviewers, ", ")

	return reviewerList
}

const teamTypeName = "Team"

const ghostName = "ghost"

// parseReviewers parses given Reviews and ReviewRequests
func parseReviewers(pr api.PullRequest) []*reviewerState {
	reviewerStates := make(map[string]*reviewerState)

	for _, review := range pr.Reviews.Nodes {
		if review.Author.Login != pr.Author.Login {
			name := review.Author.Login
			if name == "" {
				name = ghostName
			}
			reviewerStates[name] = &reviewerState{
				Name:  name,
				State: review.State,
			}
		}
	}

	// Overwrite reviewer's state if a review request for the same reviewer exists.
	for _, reviewRequest := range pr.ReviewRequests.Nodes {
		name := reviewRequest.RequestedReviewer.Login
		if reviewRequest.RequestedReviewer.TypeName == teamTypeName {
			name = reviewRequest.RequestedReviewer.Name
		}
		reviewerStates[name] = &reviewerState{
			Name:  name,
			State: requestedReviewState,
		}
	}

	// Convert map to slice for ease of sort
	result := make([]*reviewerState, 0, len(reviewerStates))
	for _, reviewer := range reviewerStates {
		if reviewer.State == pendingReviewState {
			continue
		}
		result = append(result, reviewer)
	}

	return result
}

// sortReviewerStates puts completed reviews before review requests and sorts names alphabetically
func sortReviewerStates(reviewerStates []*reviewerState) {
	sort.Slice(reviewerStates, func(i, j int) bool {
		if reviewerStates[i].State == requestedReviewState &&
			reviewerStates[j].State != requestedReviewState {
			return false
		}
		if reviewerStates[j].State == requestedReviewState &&
			reviewerStates[i].State != requestedReviewState {
			return true
		}

		return reviewerStates[i].Name < reviewerStates[j].Name
	})
}

func prAssigneeList(pr api.PullRequest) string {
	if len(pr.Assignees.Nodes) == 0 {
		return ""
	}

	AssigneeNames := make([]string, 0, len(pr.Assignees.Nodes))
	for _, assignee := range pr.Assignees.Nodes {
		AssigneeNames = append(AssigneeNames, assignee.Login)
	}

	list := strings.Join(AssigneeNames, ", ")
	if pr.Assignees.TotalCount > len(pr.Assignees.Nodes) {
		list += ", …"
	}
	return list
}

func prLabelList(pr api.PullRequest) string {
	if len(pr.Labels.Nodes) == 0 {
		return ""
	}

	labelNames := make([]string, 0, len(pr.Labels.Nodes))
	for _, label := range pr.Labels.Nodes {
		labelNames = append(labelNames, label.Name)
	}

	list := strings.Join(labelNames, ", ")
	if pr.Labels.TotalCount > len(pr.Labels.Nodes) {
		list += ", …"
	}
	return list
}

func prProjectList(pr api.PullRequest) string {
	if len(pr.ProjectCards.Nodes) == 0 {
		return ""
	}

	projectNames := make([]string, 0, len(pr.ProjectCards.Nodes))
	for _, project := range pr.ProjectCards.Nodes {
		colName := project.Column.Name
		if colName == "" {
			colName = "Awaiting triage"
		}
		projectNames = append(projectNames, fmt.Sprintf("%s (%s)", project.Project.Name, colName))
	}

	list := strings.Join(projectNames, ", ")
	if pr.ProjectCards.TotalCount > len(pr.ProjectCards.Nodes) {
		list += ", …"
	}
	return list
}

func prStateWithDraft(pr *api.PullRequest) string {
	if pr.IsDraft && pr.State == "OPEN" {
		return "DRAFT"
	}

	return pr.State
}
