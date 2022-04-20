package authflow

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/cli/cli/api"
	"github.com/cli/cli/auth"
	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/pkg/browser"
	"github.com/cli/cli/utils"
	"github.com/mattn/go-colorable"
)

var (
	// The "GitHub CLI" OAuth app
	oauthClientID = "178c6fc778ccc68e1d6a"
	// This value is safe to be embedded in version control
	oauthClientSecret = "34ddeff2b558a23d38fba8a6de74f086ede1cc0b"
)

func AuthFlowWithConfig(cfg config.Config, hostname, notice string, additionalScopes []string) (string, error) {
	// TODO this probably shouldn't live in this package. It should probably be in a new package that
	// depends on both iostreams and config.
	stderr := colorable.NewColorableStderr()

	token, userLogin, err := authFlow(hostname, stderr, notice, additionalScopes)
	if err != nil {
		return "", err
	}

	err = cfg.Set(hostname, "user", userLogin)
	if err != nil {
		return "", err
	}
	err = cfg.Set(hostname, "oauth_token", token)
	if err != nil {
		return "", err
	}

	err = cfg.Write()
	if err != nil {
		return "", err
	}

	fmt.Fprintf(stderr, "%s Authentication complete. %s to continue...\n",
		utils.GreenCheck(), utils.Bold("Press Enter"))
	_ = waitForEnter(os.Stdin)

	return token, nil
}

func authFlow(oauthHost string, w io.Writer, notice string, additionalScopes []string) (string, string, error) {
	var verboseStream io.Writer
	if strings.Contains(os.Getenv("DEBUG"), "oauth") {
		verboseStream = w
	}

	minimumScopes := []string{"repo", "read:org", "gist"}
	scopes := append(minimumScopes, additionalScopes...)

	flow := &auth.OAuthFlow{
		Hostname:     oauthHost,
		ClientID:     oauthClientID,
		ClientSecret: oauthClientSecret,
		Scopes:       scopes,
		WriteSuccessHTML: func(w io.Writer) {
			fmt.Fprintln(w, oauthSuccessPage)
		},
		VerboseStream: verboseStream,
		HTTPClient:    http.DefaultClient,
		OpenInBrowser: func(url, code string) error {
			if code != "" {
				fmt.Fprintf(w, "%s First copy your one-time code: %s\n", utils.Yellow("!"), utils.Bold(code))
			}
			fmt.Fprintf(w, "- %s to open %s in your browser... ", utils.Bold("Press Enter"), oauthHost)
			_ = waitForEnter(os.Stdin)

			browseCmd, err := browser.Command(url)
			if err != nil {
				return err
			}
			err = browseCmd.Run()
			if err != nil {
				fmt.Fprintf(w, "%s Failed opening a web browser at %s\n", utils.Red("!"), url)
				fmt.Fprintf(w, "  %s\n", err)
				fmt.Fprint(w, "  Please try entering the URL in your browser manually\n")
			}
			return nil
		},
	}

	fmt.Fprintln(w, notice)

	token, err := flow.ObtainAccessToken()
	if err != nil {
		return "", "", err
	}

	userLogin, err := getViewer(oauthHost, token)
	if err != nil {
		return "", "", err
	}

	return token, userLogin, nil
}

func getViewer(hostname, token string) (string, error) {
	http := api.NewClient(api.AddHeader("Authorization", fmt.Sprintf("token %s", token)))
	return api.CurrentLoginName(http, hostname)
}

func waitForEnter(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Scan()
	return scanner.Err()
}
