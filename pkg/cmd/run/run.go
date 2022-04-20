package run

import (
	cmdList "github.com/cli/cli/pkg/cmd/run/list"
	cmdRerun "github.com/cli/cli/pkg/cmd/run/rerun"
	cmdView "github.com/cli/cli/pkg/cmd/run/view"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdRun(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "run <command>",
		Short:  "View details about workflow runs",
		Long:   "List, view, and watch recent workflow runs from GitHub Actions.",
		Hidden: true,
		Annotations: map[string]string{
			"IsActions": "true",
		},
	}
	cmdutil.EnableRepoOverride(cmd, f)

	cmd.AddCommand(cmdList.NewCmdList(f, nil))
	cmd.AddCommand(cmdView.NewCmdView(f, nil))
	cmd.AddCommand(cmdRerun.NewCmdRerun(f, nil))

	return cmd
}
