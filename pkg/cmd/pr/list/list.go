package list

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/cmd/pr/shared"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/pkg/text"
	"github.com/cli/cli/v2/utils"
	"github.com/spf13/cobra"
)

type browser interface {
	Browse(string) error
}

type ListOptions struct {
	HttpClient func() (*http.Client, error)
	IO         *iostreams.IOStreams
	BaseRepo   func() (ghrepo.Interface, error)
	Browser    browser

	WebMode      bool
	LimitResults int
	Exporter     cmdutil.Exporter

	State      string
	BaseBranch string
	HeadBranch string
	Labels     []string
	Author     string
	Assignee   string
	Search     string
	Draft      string
}

func NewCmdList(f *cmdutil.Factory, runF func(*ListOptions) error) *cobra.Command {
	opts := &ListOptions{
		IO:         f.IOStreams,
		HttpClient: f.HttpClient,
		Browser:    f.Browser,
	}

	var draft bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List and filter pull requests in this repository",
		Example: heredoc.Doc(`
			List PRs authored by you
			$ gh pr list --author "@me"

			List PRs assigned to you
			$ gh pr list --assignee "@me"

			List PRs by label, combining multiple labels with AND
			$ gh pr list --label bug --label "priority 1"

			List PRs using search syntax
			$ gh pr list --search "status:success review:required"

			Open the list of PRs in a web browser
			$ gh pr list --web
    	`),
		Args: cmdutil.NoArgsQuoteReminder,
		RunE: func(cmd *cobra.Command, args []string) error {
			// support `-R, --repo` override
			opts.BaseRepo = f.BaseRepo

			if opts.LimitResults < 1 {
				return cmdutil.FlagErrorf("invalid value for --limit: %v", opts.LimitResults)
			}

			if cmd.Flags().Changed("draft") {
				opts.Draft = strconv.FormatBool(draft)
			}

			if runF != nil {
				return runF(opts)
			}
			return listRun(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.WebMode, "web", "w", false, "Open the browser to list the pull requests")
	cmd.Flags().IntVarP(&opts.LimitResults, "limit", "L", 30, "Maximum number of items to fetch")
	cmd.Flags().StringVarP(&opts.State, "state", "s", "open", "Filter by state: {open|closed|merged|all}")
	_ = cmd.RegisterFlagCompletionFunc("state", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"open", "closed", "merged", "all"}, cobra.ShellCompDirectiveNoFileComp
	})
	cmd.Flags().StringVarP(&opts.BaseBranch, "base", "B", "", "Filter by base branch")
	cmd.Flags().StringVarP(&opts.HeadBranch, "head", "H", "", "Filter by head branch")
	cmd.Flags().StringSliceVarP(&opts.Labels, "label", "l", nil, "Filter by labels")
	cmd.Flags().StringVarP(&opts.Author, "author", "A", "", "Filter by author")
	cmd.Flags().StringVarP(&opts.Assignee, "assignee", "a", "", "Filter by assignee")
	cmd.Flags().StringVarP(&opts.Search, "search", "S", "", "Search pull requests with `query`")
	cmd.Flags().BoolVarP(&draft, "draft", "d", false, "Filter by draft state")

	cmdutil.AddJSONFlags(cmd, &opts.Exporter, api.PullRequestFields)

	return cmd
}

var defaultFields = []string{
	"number",
	"title",
	"state",
	"url",
	"headRefName",
	"headRepositoryOwner",
	"isCrossRepository",
	"isDraft",
}

func listRun(opts *ListOptions) error {
	httpClient, err := opts.HttpClient()
	if err != nil {
		return err
	}

	baseRepo, err := opts.BaseRepo()
	if err != nil {
		return err
	}

	prState := strings.ToLower(opts.State)
	if prState == "open" && shared.QueryHasStateClause(opts.Search) {
		prState = ""
	}

	filters := shared.FilterOptions{
		Entity:     "pr",
		State:      prState,
		Author:     opts.Author,
		Assignee:   opts.Assignee,
		Labels:     opts.Labels,
		BaseBranch: opts.BaseBranch,
		HeadBranch: opts.HeadBranch,
		Search:     opts.Search,
		Draft:      opts.Draft,
		Fields:     defaultFields,
	}
	if opts.Exporter != nil {
		filters.Fields = opts.Exporter.Fields()
	}
	if opts.WebMode {
		prListURL := ghrepo.GenerateRepoURL(baseRepo, "pulls")
		openURL, err := shared.ListURLWithQuery(prListURL, filters)
		if err != nil {
			return err
		}

		if opts.IO.IsStdoutTTY() {
			fmt.Fprintf(opts.IO.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(openURL))
		}
		return opts.Browser.Browse(openURL)
	}

	listResult, err := listPullRequests(httpClient, baseRepo, filters, opts.LimitResults)
	if err != nil {
		return err
	}

	err = opts.IO.StartPager()
	if err != nil {
		fmt.Fprintf(opts.IO.ErrOut, "error starting pager: %v\n", err)
	}
	defer opts.IO.StopPager()

	if opts.Exporter != nil {
		return opts.Exporter.Write(opts.IO, listResult.PullRequests)
	}

	if listResult.SearchCapped {
		fmt.Fprintln(opts.IO.ErrOut, "warning: this query uses the Search API which is capped at 1000 results maximum")
	}
	if opts.IO.IsStdoutTTY() {
		title := shared.ListHeader(ghrepo.FullName(baseRepo), "pull request", len(listResult.PullRequests), listResult.TotalCount, !filters.IsDefault())
		fmt.Fprintf(opts.IO.Out, "\n%s\n\n", title)
	}

	cs := opts.IO.ColorScheme()
	table := utils.NewTablePrinter(opts.IO)
	for _, pr := range listResult.PullRequests {
		prNum := strconv.Itoa(pr.Number)
		if table.IsTTY() {
			prNum = "#" + prNum
		}
		table.AddField(prNum, nil, cs.ColorFromString(shared.ColorForPR(pr)))
		table.AddField(text.ReplaceExcessiveWhitespace(pr.Title), nil, nil)
		table.AddField(pr.HeadLabel(), nil, cs.Cyan)
		if !table.IsTTY() {
			table.AddField(prStateWithDraft(&pr), nil, nil)
		}
		table.EndRow()
	}
	err = table.Render()
	if err != nil {
		return err
	}

	return nil
}

func prStateWithDraft(pr *api.PullRequest) string {
	if pr.IsDraft && pr.State == "OPEN" {
		return "DRAFT"
	}

	return pr.State
}
