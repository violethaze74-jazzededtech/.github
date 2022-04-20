package create

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/cli/cli/internal/config"
	"github.com/cli/cli/internal/run"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/cli/cli/test"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func runCommand(httpClient *http.Client, cli string) (*test.CmdOut, error) {
	io, _, stdout, stderr := iostreams.Test()
	io.SetStdoutTTY(true)
	io.SetStdinTTY(true)
	fac := &cmdutil.Factory{
		IOStreams: io,
		HttpClient: func() (*http.Client, error) {
			return httpClient, nil
		},
		Config: func() (config.Config, error) {
			return config.NewBlankConfig(), nil
		},
	}

	cmd := NewCmdCreate(fac, nil)

	// TODO STUPID HACK
	// cobra aggressively adds help to all commands. since we're not running through the root command
	// (which manages help when running for real) and since create has a '-h' flag (for homepage),
	// cobra blows up when it tried to add a help flag and -h is already in use. This hack adds a
	// dummy help flag with a random shorthand to get around this.
	cmd.Flags().BoolP("help", "x", false, "")

	argv, err := shlex.Split(cli)
	cmd.SetArgs(argv)

	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err != nil {
		panic(err)
	}

	_, err = cmd.ExecuteC()

	if err != nil {
		return nil, err
	}

	return &test.CmdOut{
		OutBuf: stdout,
		ErrBuf: stderr}, nil
}

func TestRepoCreate(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.GraphQL(`mutation RepositoryCreate\b`),
		httpmock.StringResponse(`
		{ "data": { "createRepository": {
			"repository": {
				"id": "REPOID",
				"url": "https://github.com/OWNER/REPO",
				"name": "REPO",
				"owner": {
					"login": "OWNER"
				}
			}
		} } }`))

	httpClient := &http.Client{Transport: reg}

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	as, surveyTearDown := prompt.InitAskStubber()
	defer surveyTearDown()

	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "repoVisibility",
			Value: "PRIVATE",
		},
	})
	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "confirmSubmit",
			Value: true,
		},
	})

	output, err := runCommand(httpClient, "REPO")
	if err != nil {
		t.Errorf("error running command `repo create`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "✓ Created repository OWNER/REPO on GitHub\n✓ Added remote https://github.com/OWNER/REPO.git\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	assert.Equal(t, "git remote add -f origin https://github.com/OWNER/REPO.git", strings.Join(seenCmd.Args, " "))

	var reqBody struct {
		Query     string
		Variables struct {
			Input map[string]interface{}
		}
	}

	if len(reg.Requests) != 1 {
		t.Fatalf("expected 1 HTTP request, got %d", len(reg.Requests))
	}

	bodyBytes, _ := ioutil.ReadAll(reg.Requests[0].Body)
	_ = json.Unmarshal(bodyBytes, &reqBody)
	if repoName := reqBody.Variables.Input["name"].(string); repoName != "REPO" {
		t.Errorf("expected %q, got %q", "REPO", repoName)
	}
	if repoVisibility := reqBody.Variables.Input["visibility"].(string); repoVisibility != "PRIVATE" {
		t.Errorf("expected %q, got %q", "PRIVATE", repoVisibility)
	}
	if _, ownerSet := reqBody.Variables.Input["ownerId"]; ownerSet {
		t.Error("expected ownerId not to be set")
	}
}

func TestRepoCreate_org(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST("GET", "users/ORG"),
		httpmock.StringResponse(`
		{ "node_id": "ORGID"
		}`))
	reg.Register(
		httpmock.GraphQL(`mutation RepositoryCreate\b`),
		httpmock.StringResponse(`
		{ "data": { "createRepository": {
			"repository": {
				"id": "REPOID",
				"url": "https://github.com/ORG/REPO",
				"name": "REPO",
				"owner": {
					"login": "ORG"
				}
			}
		} } }`))
	httpClient := &http.Client{Transport: reg}

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	as, surveyTearDown := prompt.InitAskStubber()
	defer surveyTearDown()

	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "repoVisibility",
			Value: "PRIVATE",
		},
	})
	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "confirmSubmit",
			Value: true,
		},
	})

	output, err := runCommand(httpClient, "ORG/REPO")
	if err != nil {
		t.Errorf("error running command `repo create`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "✓ Created repository ORG/REPO on GitHub\n✓ Added remote https://github.com/ORG/REPO.git\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	assert.Equal(t, "git remote add -f origin https://github.com/ORG/REPO.git", strings.Join(seenCmd.Args, " "))

	var reqBody struct {
		Query     string
		Variables struct {
			Input map[string]interface{}
		}
	}

	if len(reg.Requests) != 2 {
		t.Fatalf("expected 2 HTTP requests, got %d", len(reg.Requests))
	}

	assert.Equal(t, "/users/ORG", reg.Requests[0].URL.Path)

	bodyBytes, _ := ioutil.ReadAll(reg.Requests[1].Body)
	_ = json.Unmarshal(bodyBytes, &reqBody)
	if orgID := reqBody.Variables.Input["ownerId"].(string); orgID != "ORGID" {
		t.Errorf("expected %q, got %q", "ORGID", orgID)
	}
	if _, teamSet := reqBody.Variables.Input["teamId"]; teamSet {
		t.Error("expected teamId not to be set")
	}
}

func TestRepoCreate_orgWithTeam(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST("GET", "orgs/ORG/teams/monkeys"),
		httpmock.StringResponse(`
		{ "node_id": "TEAMID",
			"organization": { "node_id": "ORGID" }
		}`))
	reg.Register(
		httpmock.GraphQL(`mutation RepositoryCreate\b`),
		httpmock.StringResponse(`
		{ "data": { "createRepository": {
			"repository": {
				"id": "REPOID",
				"url": "https://github.com/ORG/REPO",
				"name": "REPO",
				"owner": {
					"login": "ORG"
				}
			}
		} } }`))
	httpClient := &http.Client{Transport: reg}

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	as, surveyTearDown := prompt.InitAskStubber()
	defer surveyTearDown()

	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "repoVisibility",
			Value: "PRIVATE",
		},
	})
	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "confirmSubmit",
			Value: true,
		},
	})

	output, err := runCommand(httpClient, "ORG/REPO --team monkeys")
	if err != nil {
		t.Errorf("error running command `repo create`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "✓ Created repository ORG/REPO on GitHub\n✓ Added remote https://github.com/ORG/REPO.git\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	assert.Equal(t, "git remote add -f origin https://github.com/ORG/REPO.git", strings.Join(seenCmd.Args, " "))

	var reqBody struct {
		Query     string
		Variables struct {
			Input map[string]interface{}
		}
	}

	if len(reg.Requests) != 2 {
		t.Fatalf("expected 2 HTTP requests, got %d", len(reg.Requests))
	}

	assert.Equal(t, "/orgs/ORG/teams/monkeys", reg.Requests[0].URL.Path)

	bodyBytes, _ := ioutil.ReadAll(reg.Requests[1].Body)
	_ = json.Unmarshal(bodyBytes, &reqBody)
	if orgID := reqBody.Variables.Input["ownerId"].(string); orgID != "ORGID" {
		t.Errorf("expected %q, got %q", "ORGID", orgID)
	}
	if teamID := reqBody.Variables.Input["teamId"].(string); teamID != "TEAMID" {
		t.Errorf("expected %q, got %q", "TEAMID", teamID)
	}
}

func TestRepoCreate_template(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.GraphQL(`mutation CloneTemplateRepository\b`),
		httpmock.StringResponse(`
		{ "data": { "cloneTemplateRepository": {
			"repository": {
				"id": "REPOID",
				"name": "REPO",
				"owner": {
					"login": "OWNER"
				},
				"url": "https://github.com/OWNER/REPO"
			}
		} } }`))

	reg.StubRepoInfoResponse("OWNER", "REPO", "main")

	reg.Register(
		httpmock.GraphQL(`query UserCurrent\b`),
		httpmock.StringResponse(`{"data":{"viewer":{"ID":"OWNERID"}}}`))

	httpClient := &http.Client{Transport: reg}

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	as, surveyTearDown := prompt.InitAskStubber()
	defer surveyTearDown()

	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "repoVisibility",
			Value: "PRIVATE",
		},
	})
	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "confirmSubmit",
			Value: true,
		},
	})

	output, err := runCommand(httpClient, "REPO --template='OWNER/REPO'")
	if err != nil {
		t.Errorf("error running command `repo create`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "✓ Created repository OWNER/REPO on GitHub\n✓ Added remote https://github.com/OWNER/REPO.git\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	assert.Equal(t, "git remote add -f origin https://github.com/OWNER/REPO.git", strings.Join(seenCmd.Args, " "))

	var reqBody struct {
		Query     string
		Variables struct {
			Input map[string]interface{}
		}
	}

	if len(reg.Requests) != 3 {
		t.Fatalf("expected 3 HTTP requests, got %d", len(reg.Requests))
	}

	bodyBytes, _ := ioutil.ReadAll(reg.Requests[2].Body)
	_ = json.Unmarshal(bodyBytes, &reqBody)
	if repoName := reqBody.Variables.Input["name"].(string); repoName != "REPO" {
		t.Errorf("expected %q, got %q", "REPO", repoName)
	}
	if repoVisibility := reqBody.Variables.Input["visibility"].(string); repoVisibility != "PRIVATE" {
		t.Errorf("expected %q, got %q", "PRIVATE", repoVisibility)
	}
	if ownerId := reqBody.Variables.Input["ownerId"].(string); ownerId != "OWNERID" {
		t.Errorf("expected %q, got %q", "OWNERID", ownerId)
	}
}

func TestRepoCreate_withoutNameArg(t *testing.T) {
	reg := &httpmock.Registry{}
	reg.Register(
		httpmock.REST("GET", "users/OWNER"),
		httpmock.StringResponse(`{ "node_id": "OWNERID" }`))
	reg.Register(
		httpmock.GraphQL(`mutation RepositoryCreate\b`),
		httpmock.StringResponse(`
		{ "data": { "createRepository": {
			"repository": {
				"id": "REPOID",
				"url": "https://github.com/OWNER/REPO",
				"name": "REPO",
				"owner": {
					"login": "OWNER"
				}
			}
		} } }`))
	httpClient := &http.Client{Transport: reg}

	var seenCmd *exec.Cmd
	restoreCmd := run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
		seenCmd = cmd
		return &test.OutputStub{}
	})
	defer restoreCmd()

	as, surveyTearDown := prompt.InitAskStubber()
	defer surveyTearDown()

	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "repoName",
			Value: "OWNER/REPO",
		},
		{
			Name:  "repoDescription",
			Value: "DESCRIPTION",
		},
		{
			Name:  "repoVisibility",
			Value: "PRIVATE",
		},
	})
	as.Stub([]*prompt.QuestionStub{
		{
			Name:  "confirmSubmit",
			Value: true,
		},
	})

	output, err := runCommand(httpClient, "")
	if err != nil {
		t.Errorf("error running command `repo create`: %v", err)
	}

	assert.Equal(t, "", output.String())
	assert.Equal(t, "✓ Created repository OWNER/REPO on GitHub\n✓ Added remote https://github.com/OWNER/REPO.git\n", output.Stderr())

	if seenCmd == nil {
		t.Fatal("expected a command to run")
	}
	assert.Equal(t, "git remote add -f origin https://github.com/OWNER/REPO.git", strings.Join(seenCmd.Args, " "))

	var reqBody struct {
		Query     string
		Variables struct {
			Input map[string]interface{}
		}
	}

	if len(reg.Requests) != 2 {
		t.Fatalf("expected 2 HTTP request, got %d", len(reg.Requests))
	}

	bodyBytes, _ := ioutil.ReadAll(reg.Requests[1].Body)
	_ = json.Unmarshal(bodyBytes, &reqBody)
	if repoName := reqBody.Variables.Input["name"].(string); repoName != "REPO" {
		t.Errorf("expected %q, got %q", "REPO", repoName)
	}
	if repoVisibility := reqBody.Variables.Input["visibility"].(string); repoVisibility != "PRIVATE" {
		t.Errorf("expected %q, got %q", "PRIVATE", repoVisibility)
	}
	if ownerId := reqBody.Variables.Input["ownerId"].(string); ownerId != "OWNERID" {
		t.Errorf("expected %q, got %q", "OWNERID", ownerId)
	}
}
