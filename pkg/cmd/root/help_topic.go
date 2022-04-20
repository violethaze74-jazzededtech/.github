package root

import (
	"github.com/MakeNowJust/heredoc"
	"github.com/spf13/cobra"
)

var HelpTopics = map[string]map[string]string{
	"environment": {
		"short": "Environment variables that can be used with gh",
		"long": heredoc.Doc(`
			GH_TOKEN, GITHUB_TOKEN (in order of precedence): an authentication token for github.com
			API requests. Setting this avoids being prompted to authenticate and takes precedence over
			previously stored credentials.

			GH_ENTERPRISE_TOKEN, GITHUB_ENTERPRISE_TOKEN (in order of precedence): an authentication
			token for API requests to GitHub Enterprise.

			GH_REPO: specify the GitHub repository in the "[HOST/]OWNER/REPO" format for commands
			that otherwise operate on a local repository.

			GH_HOST: specify the GitHub hostname for commands that would otherwise assume
			the "github.com" host when not in a context of an existing repository.

			GH_EDITOR, GIT_EDITOR, VISUAL, EDITOR (in order of precedence): the editor tool to use
			for authoring text.

			BROWSER: the web browser to use for opening links.

			DEBUG: set to any value to enable verbose output to standard error. Include values "api"
			or "oauth" to print detailed information about HTTP requests or authentication flow.

			GH_PAGER, PAGER (in order of precedence): a terminal paging program to send standard output to, e.g. "less".

			GLAMOUR_STYLE: the style to use for rendering Markdown. See
			https://github.com/charmbracelet/glamour#styles

			NO_COLOR: set to any value to avoid printing ANSI escape sequences for color output.

			CLICOLOR: set to "0" to disable printing ANSI colors in output.

			CLICOLOR_FORCE: set to a value other than "0" to keep ANSI colors in output
			even when the output is piped.

			GH_NO_UPDATE_NOTIFIER: set to any value to disable update notifications. By default, gh
			checks for new releases once every 24 hours and displays an upgrade notice on standard
			error if a newer version was found.
		`),
	},
	"reference": {
		"short": "A comprehensive reference of all gh commands",
	},
	"formatting": {
		"short": "Formatting options for JSON data exported from gh",
		"long": heredoc.Docf(`
			Some gh commands support exporting the data as JSON as an alternative to their usual
			line-based plain text output. This is suitable for passing structured data to scripts.
			The JSON output is enabled with the %[1]s--json%[1]s option, followed by the list of fields
			to fetch. Use the flag without a value to get the list of available fields.

			The %[1]s--jq%[1]s option accepts a query in jq syntax and will print only the resulting
			values that match the query. This is equivalent to piping the output to %[1]sjq -r%[1]s,
			but does not require the jq utility to be installed on the system. To learn more
			about the query syntax, see: https://stedolan.github.io/jq/manual/v1.6/

			With %[1]s--template%[1]s, the provided Go template is rendered using the JSON data as input.
			For the syntax of Go templates, see: https://golang.org/pkg/text/template/

			The following functions are available in templates:
			- %[1]scolor <style>, <input>%[1]s: colorize input using https://github.com/mgutz/ansi
			- %[1]sautocolor%[1]s: like %[1]scolor%[1]s, but only emits color to terminals
			- %[1]stimefmt <format> <time>%[1]s: formats a timestamp using Go's Time.Format function
			- %[1]stimeago <time>%[1]s: renders a timestamp as relative to now
			- %[1]spluck <field> <list>%[1]s: collects values of a field from all items in the input
			- %[1]sjoin <sep> <list>%[1]s: joins values in the list using a separator
		`, "`"),
	},
}

func NewHelpTopic(topic string) *cobra.Command {
	cmd := &cobra.Command{
		Use:    topic,
		Short:  HelpTopics[topic]["short"],
		Long:   HelpTopics[topic]["long"],
		Hidden: true,
		Annotations: map[string]string{
			"markdown:generate": "true",
			"markdown:basename": "gh_help_" + topic,
		},
	}

	cmd.SetHelpFunc(helpTopicHelpFunc)
	cmd.SetUsageFunc(helpTopicUsageFunc)

	return cmd
}

func helpTopicHelpFunc(command *cobra.Command, args []string) {
	command.Print(command.Long)
}

func helpTopicUsageFunc(command *cobra.Command) error {
	command.Printf("Usage: gh help %s", command.Use)
	return nil
}
