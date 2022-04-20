package shared

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/cli/cli/api"
	"github.com/cli/cli/git"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/githubtemplate"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/cli/cli/pkg/surveyext"
	"github.com/cli/cli/utils"
)

type Action int

const (
	SubmitAction Action = iota
	PreviewAction
	CancelAction
	MetadataAction

	noMilestone = "(none)"
)

func ConfirmSubmission(allowPreview bool, allowMetadata bool) (Action, error) {
	const (
		submitLabel   = "Submit"
		previewLabel  = "Continue in browser"
		metadataLabel = "Add metadata"
		cancelLabel   = "Cancel"
	)

	options := []string{submitLabel}
	if allowPreview {
		options = append(options, previewLabel)
	}
	if allowMetadata {
		options = append(options, metadataLabel)
	}
	options = append(options, cancelLabel)

	confirmAnswers := struct {
		Confirmation int
	}{}
	confirmQs := []*survey.Question{
		{
			Name: "confirmation",
			Prompt: &survey.Select{
				Message: "What's next?",
				Options: options,
			},
		},
	}

	err := prompt.SurveyAsk(confirmQs, &confirmAnswers)
	if err != nil {
		return -1, fmt.Errorf("could not prompt: %w", err)
	}

	switch options[confirmAnswers.Confirmation] {
	case submitLabel:
		return SubmitAction, nil
	case previewLabel:
		return PreviewAction, nil
	case metadataLabel:
		return MetadataAction, nil
	case cancelLabel:
		return CancelAction, nil
	default:
		return -1, fmt.Errorf("invalid index: %d", confirmAnswers.Confirmation)
	}
}

func TemplateSurvey(templateFiles []string, legacyTemplate string, state IssueMetadataState) (templateContent string, err error) {
	if len(templateFiles) == 0 && legacyTemplate == "" {
		return
	}

	if len(templateFiles) > 0 {
		templateContent, err = selectTemplate(templateFiles, legacyTemplate, state.Type)
	} else {
		templateContent = string(githubtemplate.ExtractContents(legacyTemplate))
	}

	return
}

func selectTemplate(nonLegacyTemplatePaths []string, legacyTemplatePath string, metadataType metadataStateType) (string, error) {
	templateResponse := struct {
		Index int
	}{}
	templateNames := make([]string, 0, len(nonLegacyTemplatePaths))
	for _, p := range nonLegacyTemplatePaths {
		templateNames = append(templateNames, githubtemplate.ExtractName(p))
	}
	if metadataType == IssueMetadata {
		templateNames = append(templateNames, "Open a blank issue")
	} else if metadataType == PRMetadata {
		templateNames = append(templateNames, "Open a blank pull request")
	}

	selectQs := []*survey.Question{
		{
			Name: "index",
			Prompt: &survey.Select{
				Message: "Choose a template",
				Options: templateNames,
			},
		},
	}
	if err := prompt.SurveyAsk(selectQs, &templateResponse); err != nil {
		return "", fmt.Errorf("could not prompt: %w", err)
	}

	if templateResponse.Index == len(nonLegacyTemplatePaths) { // the user has selected the blank template
		if legacyTemplatePath != "" {
			templateContents := githubtemplate.ExtractContents(legacyTemplatePath)
			return string(templateContents), nil
		} else {
			return "", nil
		}
	}
	templateContents := githubtemplate.ExtractContents(nonLegacyTemplatePaths[templateResponse.Index])
	return string(templateContents), nil
}

func BodySurvey(state *IssueMetadataState, templateContent, editorCommand string) error {
	if templateContent != "" {
		if state.Body != "" {
			// prevent excessive newlines between default body and template
			state.Body = strings.TrimRight(state.Body, "\n")
			state.Body += "\n\n"
		}
		state.Body += templateContent
	}

	preBody := state.Body

	// TODO should just be an AskOne but ran into problems with the stubber
	qs := []*survey.Question{
		{
			Name: "Body",
			Prompt: &surveyext.GhEditor{
				BlankAllowed:  true,
				EditorCommand: editorCommand,
				Editor: &survey.Editor{
					Message:       "Body",
					FileName:      "*.md",
					Default:       state.Body,
					HideDefault:   true,
					AppendDefault: true,
				},
			},
		},
	}

	err := prompt.SurveyAsk(qs, state)
	if err != nil {
		return err
	}

	if state.Body != "" && preBody != state.Body {
		state.MarkDirty()
	}

	return nil
}

func TitleSurvey(state *IssueMetadataState) error {
	preTitle := state.Title

	// TODO should just be an AskOne but ran into problems with the stubber
	qs := []*survey.Question{
		{
			Name: "Title",
			Prompt: &survey.Input{
				Message: "Title",
				Default: state.Title,
			},
		},
	}

	err := prompt.SurveyAsk(qs, state)
	if err != nil {
		return err
	}

	if preTitle != state.Title {
		state.MarkDirty()
	}

	return nil
}

func MetadataSurvey(io *iostreams.IOStreams, client *api.Client, baseRepo ghrepo.Interface, state *IssueMetadataState) error {
	isChosen := func(m string) bool {
		for _, c := range state.Metadata {
			if m == c {
				return true
			}
		}
		return false
	}

	allowReviewers := state.Type == PRMetadata

	extraFieldsOptions := []string{}
	if allowReviewers {
		extraFieldsOptions = append(extraFieldsOptions, "Reviewers")
	}
	extraFieldsOptions = append(extraFieldsOptions, "Assignees", "Labels", "Projects", "Milestone")

	err := prompt.SurveyAsk([]*survey.Question{
		{
			Name: "metadata",
			Prompt: &survey.MultiSelect{
				Message: "What would you like to add?",
				Options: extraFieldsOptions,
			},
		},
	}, state)
	if err != nil {
		return fmt.Errorf("could not prompt: %w", err)
	}

	metadataInput := api.RepoMetadataInput{
		Reviewers:  isChosen("Reviewers"),
		Assignees:  isChosen("Assignees"),
		Labels:     isChosen("Labels"),
		Projects:   isChosen("Projects"),
		Milestones: isChosen("Milestone"),
	}
	s := utils.Spinner(io.ErrOut)
	utils.StartSpinner(s)
	state.MetadataResult, err = api.RepoMetadata(client, baseRepo, metadataInput)
	utils.StopSpinner(s)
	if err != nil {
		return fmt.Errorf("error fetching metadata options: %w", err)
	}

	var users []string
	for _, u := range state.MetadataResult.AssignableUsers {
		users = append(users, u.Login)
	}
	var teams []string
	for _, t := range state.MetadataResult.Teams {
		teams = append(teams, fmt.Sprintf("%s/%s", baseRepo.RepoOwner(), t.Slug))
	}
	var labels []string
	for _, l := range state.MetadataResult.Labels {
		labels = append(labels, l.Name)
	}
	var projects []string
	for _, l := range state.MetadataResult.Projects {
		projects = append(projects, l.Name)
	}
	milestones := []string{noMilestone}
	for _, m := range state.MetadataResult.Milestones {
		milestones = append(milestones, m.Title)
	}

	type metadataValues struct {
		Reviewers []string
		Assignees []string
		Labels    []string
		Projects  []string
		Milestone string
	}
	var mqs []*survey.Question
	if isChosen("Reviewers") {
		if len(users) > 0 || len(teams) > 0 {
			mqs = append(mqs, &survey.Question{
				Name: "reviewers",
				Prompt: &survey.MultiSelect{
					Message: "Reviewers",
					Options: append(users, teams...),
					Default: state.Reviewers,
				},
			})
		} else {
			fmt.Fprintln(io.ErrOut, "warning: no available reviewers")
		}
	}
	if isChosen("Assignees") {
		if len(users) > 0 {
			mqs = append(mqs, &survey.Question{
				Name: "assignees",
				Prompt: &survey.MultiSelect{
					Message: "Assignees",
					Options: users,
					Default: state.Assignees,
				},
			})
		} else {
			fmt.Fprintln(io.ErrOut, "warning: no assignable users")
		}
	}
	if isChosen("Labels") {
		if len(labels) > 0 {
			mqs = append(mqs, &survey.Question{
				Name: "labels",
				Prompt: &survey.MultiSelect{
					Message: "Labels",
					Options: labels,
					Default: state.Labels,
				},
			})
		} else {
			fmt.Fprintln(io.ErrOut, "warning: no labels in the repository")
		}
	}
	if isChosen("Projects") {
		if len(projects) > 0 {
			mqs = append(mqs, &survey.Question{
				Name: "projects",
				Prompt: &survey.MultiSelect{
					Message: "Projects",
					Options: projects,
					Default: state.Projects,
				},
			})
		} else {
			fmt.Fprintln(io.ErrOut, "warning: no projects to choose from")
		}
	}
	if isChosen("Milestone") {
		if len(milestones) > 1 {
			var milestoneDefault string
			if len(state.Milestones) > 0 {
				milestoneDefault = state.Milestones[0]
			}
			mqs = append(mqs, &survey.Question{
				Name: "milestone",
				Prompt: &survey.Select{
					Message: "Milestone",
					Options: milestones,
					Default: milestoneDefault,
				},
			})
		} else {
			fmt.Fprintln(io.ErrOut, "warning: no milestones in the repository")
		}
	}
	values := metadataValues{}
	err = prompt.SurveyAsk(mqs, &values, survey.WithKeepFilter(true))
	if err != nil {
		return fmt.Errorf("could not prompt: %w", err)
	}
	state.Reviewers = values.Reviewers
	state.Assignees = values.Assignees
	state.Labels = values.Labels
	state.Projects = values.Projects
	if values.Milestone != "" && values.Milestone != noMilestone {
		state.Milestones = []string{values.Milestone}
	}

	return nil
}

func FindTemplates(dir, path string) ([]string, string) {
	if dir == "" {
		rootDir, err := git.ToplevelDir()
		if err != nil {
			return []string{}, ""
		}
		dir = rootDir
	}

	templateFiles := githubtemplate.FindNonLegacy(dir, path)
	legacyTemplate := githubtemplate.FindLegacy(dir, path)

	return templateFiles, legacyTemplate
}
