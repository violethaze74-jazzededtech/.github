package set

import (
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/google/shlex"
	"github.com/spf13/cobra"
)

type SetOptions struct {
	Config func() (config.Config, error)
	IO     *iostreams.IOStreams

	Name      string
	Expansion string
	IsShell   bool
	RootCmd   *cobra.Command
}

func NewCmdSet(f *cmdutil.Factory, runF func(*SetOptions) error) *cobra.Command {
	opts := &SetOptions{
		IO:     f.IOStreams,
		Config: f.Config,
	}

	cmd := &cobra.Command{
		Use:   "set <alias> <expansion>",
		Short: "Create a shortcut for a gh command",
		Long: heredoc.Doc(`
			Declare a word as a command alias that will expand to the specified command(s).

			The expansion may specify additional arguments and flags. If the expansion
			includes positional placeholders such as '$1', '$2', etc., any extra arguments
			that follow the invocation of an alias will be inserted appropriately.
			Reads from STDIN if '-' is specified as the expansion parameter. This can be useful
			for commands with mixed quotes or multiple lines.

			If '--shell' is specified, the alias will be run through a shell interpreter (sh). This allows you
			to compose commands with "|" or redirect with ">". Note that extra arguments following the alias
			will not be automatically passed to the expanded expression. To have a shell alias receive
			arguments, you must explicitly accept them using "$1", "$2", etc., or "$@" to accept all of them.

			Platform note: on Windows, shell aliases are executed via "sh" as installed by Git For Windows. If
			you have installed git on Windows in some other way, shell aliases may not work for you.

			Quotes must always be used when defining a command as in the examples unless you pass '-'
			as the expansion parameter and pipe your command to 'gh alias set'.
		`),
		Example: heredoc.Doc(`
			$ gh alias set pv 'pr view'
			$ gh pv -w 123
			#=> gh pr view -w 123
			
			$ gh alias set bugs 'issue list --label="bugs"'
			$ gh bugs

			$ gh alias set homework 'issue list --assignee @me'
			$ gh homework

			$ gh alias set epicsBy 'issue list --author="$1" --label="epic"'
			$ gh epicsBy vilmibm
			#=> gh issue list --author="vilmibm" --label="epic"

			$ gh alias set --shell igrep 'gh issue list --label="$1" | grep $2'
			$ gh igrep epic foo
			#=> gh issue list --label="epic" | grep "foo"

			# users.txt contains multiline 'api graphql -F name="$1" ...' with mixed quotes
			$ gh alias set users - < users.txt
			$ gh users octocat
			#=> gh api graphql -F name="octocat" ...
		`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.RootCmd = cmd.Root()

			opts.Name = args[0]
			opts.Expansion = args[1]

			if runF != nil {
				return runF(opts)
			}
			return setRun(opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.IsShell, "shell", "s", false, "Declare an alias to be passed through a shell interpreter")

	return cmd
}

func setRun(opts *SetOptions) error {
	cs := opts.IO.ColorScheme()
	cfg, err := opts.Config()
	if err != nil {
		return err
	}

	aliasCfg, err := cfg.Aliases()
	if err != nil {
		return err
	}

	expansion, err := getExpansion(opts)
	if err != nil {
		return fmt.Errorf("did not understand expansion: %w", err)
	}

	isTerminal := opts.IO.IsStdoutTTY()
	if isTerminal {
		fmt.Fprintf(opts.IO.ErrOut, "- Adding alias for %s: %s\n", cs.Bold(opts.Name), cs.Bold(expansion))
	}

	isShell := opts.IsShell
	if isShell && !strings.HasPrefix(expansion, "!") {
		expansion = "!" + expansion
	}
	isShell = strings.HasPrefix(expansion, "!")

	if validCommand(opts.RootCmd, opts.Name) {
		return fmt.Errorf("could not create alias: %q is already a gh command", opts.Name)
	}

	if !isShell && !validCommand(opts.RootCmd, expansion) {
		return fmt.Errorf("could not create alias: %s does not correspond to a gh command", expansion)
	}

	successMsg := fmt.Sprintf("%s Added alias.", cs.SuccessIcon())
	if oldExpansion, ok := aliasCfg.Get(opts.Name); ok {
		successMsg = fmt.Sprintf("%s Changed alias %s from %s to %s",
			cs.SuccessIcon(),
			cs.Bold(opts.Name),
			cs.Bold(oldExpansion),
			cs.Bold(expansion),
		)
	}

	err = aliasCfg.Add(opts.Name, expansion)
	if err != nil {
		return fmt.Errorf("could not create alias: %s", err)
	}

	if isTerminal {
		fmt.Fprintln(opts.IO.ErrOut, successMsg)
	}

	return nil
}

func validCommand(rootCmd *cobra.Command, expansion string) bool {
	split, err := shlex.Split(expansion)
	if err != nil {
		return false
	}

	cmd, _, err := rootCmd.Traverse(split)
	return err == nil && cmd != rootCmd
}

func getExpansion(opts *SetOptions) (string, error) {
	if opts.Expansion == "-" {
		stdin, err := ioutil.ReadAll(opts.IO.In)
		if err != nil {
			return "", fmt.Errorf("failed to read from STDIN: %w", err)
		}

		return string(stdin), nil
	}

	return opts.Expansion, nil
}
