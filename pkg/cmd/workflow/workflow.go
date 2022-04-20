package workflow

import (
	cmdDisable "github.com/cli/cli/pkg/cmd/workflow/disable"
	cmdEnable "github.com/cli/cli/pkg/cmd/workflow/enable"
	cmdList "github.com/cli/cli/pkg/cmd/workflow/list"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

func NewCmdWorkflow(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "workflow <command>",
		Short:  "View details about GitHub Actions workflows",
		Long:   "List, view, and run workflows in GitHub Actions.",
		Hidden: true,
		Annotations: map[string]string{
			"IsActions": "true",
		},
	}
	cmdutil.EnableRepoOverride(cmd, f)

	cmd.AddCommand(cmdList.NewCmdList(f, nil))
	cmd.AddCommand(cmdEnable.NewCmdEnable(f, nil))
	cmd.AddCommand(cmdDisable.NewCmdDisable(f, nil))

	return cmd
}
