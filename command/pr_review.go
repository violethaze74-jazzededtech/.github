package command

import (
	"errors"
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/MakeNowJust/heredoc"
	"github.com/spf13/cobra"

	"github.com/cli/cli/api"
	"github.com/cli/cli/pkg/surveyext"
	"github.com/cli/cli/utils"
)

func init() {
	prCmd.AddCommand(prReviewCmd)

	prReviewCmd.Flags().BoolP("approve", "a", false, "Approve pull request")
	prReviewCmd.Flags().BoolP("request-changes", "r", false, "Request changes on a pull request")
	prReviewCmd.Flags().BoolP("comment", "c", false, "Comment on a pull request")
	prReviewCmd.Flags().StringP("body", "b", "", "Specify the body of a review")
}

var prReviewCmd = &cobra.Command{
	Use:   "review [<number> | <url> | <branch>]",
	Short: "Add a review to a pull request",
	Long: `Add a review to a pull request.

Without an argument, the pull request that belongs to the current branch is reviewed.`,
	Example: heredoc.Doc(`
	# approve the pull request of the current branch
	$ gh pr review --approve
	
	# leave a review comment for the current branch
	$ gh pr review --comment -b "interesting"
	
	# add a review for a specific pull request
	$ gh pr review 123
	
	# request changes on a specific pull request
	$ gh pr review 123 -r -b "needs more ASCII art"
	`),
	Args: cobra.MaximumNArgs(1),
	RunE: prReview,
}

func processReviewOpt(cmd *cobra.Command) (*api.PullRequestReviewInput, error) {
	found := 0
	flag := ""
	var state api.PullRequestReviewState

	if cmd.Flags().Changed("approve") {
		found++
		flag = "approve"
		state = api.ReviewApprove
	}
	if cmd.Flags().Changed("request-changes") {
		found++
		flag = "request-changes"
		state = api.ReviewRequestChanges
	}
	if cmd.Flags().Changed("comment") {
		found++
		flag = "comment"
		state = api.ReviewComment
	}

	body, err := cmd.Flags().GetString("body")
	if err != nil {
		return nil, err
	}

	if found == 0 && body == "" {
		return nil, nil // signal interactive mode
	} else if found == 0 && body != "" {
		return nil, errors.New("--body unsupported without --approve, --request-changes, or --comment")
	} else if found > 1 {
		return nil, errors.New("need exactly one of --approve, --request-changes, or --comment")
	}

	if (flag == "request-changes" || flag == "comment") && body == "" {
		return nil, fmt.Errorf("body cannot be blank for %s review", flag)
	}

	return &api.PullRequestReviewInput{
		Body:  body,
		State: state,
	}, nil
}

func prReview(cmd *cobra.Command, args []string) error {
	ctx := contextForCommand(cmd)
	apiClient, err := apiClientForContext(ctx)
	if err != nil {
		return err
	}

	pr, _, err := prFromArgs(ctx, apiClient, cmd, args)
	if err != nil {
		return err
	}

	reviewData, err := processReviewOpt(cmd)
	if err != nil {
		return fmt.Errorf("did not understand desired review action: %w", err)
	}

	out := colorableOut(cmd)

	if reviewData == nil {
		reviewData, err = reviewSurvey(cmd)
		if err != nil {
			return err
		}
		if reviewData == nil && err == nil {
			fmt.Fprint(out, "Discarding.\n")
			return nil
		}
	}

	err = api.AddReview(apiClient, pr, reviewData)
	if err != nil {
		return fmt.Errorf("failed to create review: %w", err)
	}

	switch reviewData.State {
	case api.ReviewComment:
		fmt.Fprintf(out, "%s Reviewed pull request #%d\n", utils.Gray("-"), pr.Number)
	case api.ReviewApprove:
		fmt.Fprintf(out, "%s Approved pull request #%d\n", utils.Green("✓"), pr.Number)
	case api.ReviewRequestChanges:
		fmt.Fprintf(out, "%s Requested changes to pull request #%d\n", utils.Red("+"), pr.Number)
	}

	return nil
}

func reviewSurvey(cmd *cobra.Command) (*api.PullRequestReviewInput, error) {
	editorCommand, err := determineEditor(cmd)
	if err != nil {
		return nil, err
	}

	typeAnswers := struct {
		ReviewType string
	}{}
	typeQs := []*survey.Question{
		{
			Name: "reviewType",
			Prompt: &survey.Select{
				Message: "What kind of review do you want to give?",
				Options: []string{
					"Comment",
					"Approve",
					"Request changes",
				},
			},
		},
	}

	err = SurveyAsk(typeQs, &typeAnswers)
	if err != nil {
		return nil, err
	}

	var reviewState api.PullRequestReviewState

	switch typeAnswers.ReviewType {
	case "Approve":
		reviewState = api.ReviewApprove
	case "Request changes":
		reviewState = api.ReviewRequestChanges
	case "Comment":
		reviewState = api.ReviewComment
	default:
		panic("unreachable state")
	}

	bodyAnswers := struct {
		Body string
	}{}

	blankAllowed := false
	if reviewState == api.ReviewApprove {
		blankAllowed = true
	}

	bodyQs := []*survey.Question{
		{
			Name: "body",
			Prompt: &surveyext.GhEditor{
				BlankAllowed:  blankAllowed,
				EditorCommand: editorCommand,
				Editor: &survey.Editor{
					Message:  "Review body",
					FileName: "*.md",
				},
			},
		},
	}

	err = SurveyAsk(bodyQs, &bodyAnswers)
	if err != nil {
		return nil, err
	}

	if bodyAnswers.Body == "" && (reviewState == api.ReviewComment || reviewState == api.ReviewRequestChanges) {
		return nil, errors.New("this type of review cannot be blank")
	}

	if len(bodyAnswers.Body) > 0 {
		out := colorableOut(cmd)
		renderedBody, err := utils.RenderMarkdown(bodyAnswers.Body)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(out, "Got:\n%s", renderedBody)
	}

	confirm := false
	confirmQs := []*survey.Question{
		{
			Name: "confirm",
			Prompt: &survey.Confirm{
				Message: "Submit?",
				Default: true,
			},
		},
	}

	err = SurveyAsk(confirmQs, &confirm)
	if err != nil {
		return nil, err
	}

	if !confirm {
		return nil, nil
	}

	return &api.PullRequestReviewInput{
		Body:  bodyAnswers.Body,
		State: reviewState,
	}, nil
}
