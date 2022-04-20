package api

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/cli/v2/internal/ghrepo"
	"github.com/cli/cli/v2/pkg/httpmock"
)

func TestGitHubRepo_notFound(t *testing.T) {
	httpReg := &httpmock.Registry{}
	defer httpReg.Verify(t)

	httpReg.Register(
		httpmock.GraphQL(`query RepositoryInfo\b`),
		httpmock.StringResponse(`{ "data": { "repository": null } }`))

	client := NewClient(ReplaceTripper(httpReg))
	repo, err := GitHubRepo(client, ghrepo.New("OWNER", "REPO"))
	if err == nil {
		t.Fatal("GitHubRepo did not return an error")
	}
	if wants := "GraphQL: Could not resolve to a Repository with the name 'OWNER/REPO'."; err.Error() != wants {
		t.Errorf("GitHubRepo error: want %q, got %q", wants, err.Error())
	}
	if repo != nil {
		t.Errorf("GitHubRepo: expected nil repo, got %v", repo)
	}
}

func Test_RepoMetadata(t *testing.T) {
	http := &httpmock.Registry{}
	client := NewClient(ReplaceTripper(http))

	repo, _ := ghrepo.FromFullName("OWNER/REPO")
	input := RepoMetadataInput{
		Assignees:  true,
		Reviewers:  true,
		Labels:     true,
		Projects:   true,
		Milestones: true,
	}

	http.Register(
		httpmock.GraphQL(`query RepositoryAssignableUsers\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "assignableUsers": {
			"nodes": [
				{ "login": "hubot", "id": "HUBOTID" },
				{ "login": "MonaLisa", "id": "MONAID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryLabelList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "labels": {
			"nodes": [
				{ "name": "feature", "id": "FEATUREID" },
				{ "name": "TODO", "id": "TODOID" },
				{ "name": "bug", "id": "BUGID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryMilestoneList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "milestones": {
			"nodes": [
				{ "title": "GA", "id": "GAID" },
				{ "title": "Big One.oh", "id": "BIGONEID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query RepositoryProjectList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "projects": {
			"nodes": [
				{ "name": "Cleanup", "id": "CLEANUPID" },
				{ "name": "Roadmap", "id": "ROADMAPID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query OrganizationProjectList\b`),
		httpmock.StringResponse(`
		{ "data": { "organization": { "projects": {
			"nodes": [
				{ "name": "Triage", "id": "TRIAGEID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query OrganizationTeamList\b`),
		httpmock.StringResponse(`
		{ "data": { "organization": { "teams": {
			"nodes": [
				{ "slug": "owners", "id": "OWNERSID" },
				{ "slug": "Core", "id": "COREID" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))

	result, err := RepoMetadata(client, repo, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedMemberIDs := []string{"MONAID", "HUBOTID"}
	memberIDs, err := result.MembersToIDs([]string{"monalisa", "hubot"})
	if err != nil {
		t.Errorf("error resolving members: %v", err)
	}
	if !sliceEqual(memberIDs, expectedMemberIDs) {
		t.Errorf("expected members %v, got %v", expectedMemberIDs, memberIDs)
	}

	expectedTeamIDs := []string{"COREID", "OWNERSID"}
	teamIDs, err := result.TeamsToIDs([]string{"OWNER/core", "/owners"})
	if err != nil {
		t.Errorf("error resolving teams: %v", err)
	}
	if !sliceEqual(teamIDs, expectedTeamIDs) {
		t.Errorf("expected teams %v, got %v", expectedTeamIDs, teamIDs)
	}

	expectedLabelIDs := []string{"BUGID", "TODOID"}
	labelIDs, err := result.LabelsToIDs([]string{"bug", "todo"})
	if err != nil {
		t.Errorf("error resolving labels: %v", err)
	}
	if !sliceEqual(labelIDs, expectedLabelIDs) {
		t.Errorf("expected labels %v, got %v", expectedLabelIDs, labelIDs)
	}

	expectedProjectIDs := []string{"TRIAGEID", "ROADMAPID"}
	projectIDs, err := result.ProjectsToIDs([]string{"triage", "roadmap"})
	if err != nil {
		t.Errorf("error resolving projects: %v", err)
	}
	if !sliceEqual(projectIDs, expectedProjectIDs) {
		t.Errorf("expected projects %v, got %v", expectedProjectIDs, projectIDs)
	}

	expectedMilestoneID := "BIGONEID"
	milestoneID, err := result.MilestoneToID("big one.oh")
	if err != nil {
		t.Errorf("error resolving milestone: %v", err)
	}
	if milestoneID != expectedMilestoneID {
		t.Errorf("expected milestone %v, got %v", expectedMilestoneID, milestoneID)
	}
}

func Test_ProjectsToPaths(t *testing.T) {
	expectedProjectPaths := []string{"OWNER/REPO/PROJECT_NUMBER", "ORG/PROJECT_NUMBER"}
	projects := []RepoProject{
		{ID: "id1", Name: "My Project", ResourcePath: "/OWNER/REPO/projects/PROJECT_NUMBER"},
		{ID: "id2", Name: "Org Project", ResourcePath: "/orgs/ORG/projects/PROJECT_NUMBER"},
		{ID: "id3", Name: "Project", ResourcePath: "/orgs/ORG/projects/PROJECT_NUMBER_2"},
	}
	projectNames := []string{"My Project", "Org Project"}

	projectPaths, err := ProjectsToPaths(projects, projectNames)
	if err != nil {
		t.Errorf("error resolving projects: %v", err)
	}
	if !sliceEqual(projectPaths, expectedProjectPaths) {
		t.Errorf("expected projects %v, got %v", expectedProjectPaths, projectPaths)
	}
}

func Test_ProjectNamesToPaths(t *testing.T) {
	http := &httpmock.Registry{}
	client := NewClient(ReplaceTripper(http))

	repo, _ := ghrepo.FromFullName("OWNER/REPO")

	http.Register(
		httpmock.GraphQL(`query RepositoryProjectList\b`),
		httpmock.StringResponse(`
		{ "data": { "repository": { "projects": {
			"nodes": [
				{ "name": "Cleanup", "id": "CLEANUPID", "resourcePath": "/OWNER/REPO/projects/1" },
				{ "name": "Roadmap", "id": "ROADMAPID", "resourcePath": "/OWNER/REPO/projects/2" }
			],
			"pageInfo": { "hasNextPage": false }
		} } } }
		`))
	http.Register(
		httpmock.GraphQL(`query OrganizationProjectList\b`),
		httpmock.StringResponse(`
			{ "data": { "organization": { "projects": {
				"nodes": [
					{ "name": "Triage", "id": "TRIAGEID", "resourcePath": "/orgs/ORG/projects/1"  }
				],
				"pageInfo": { "hasNextPage": false }
			} } } }
			`))

	projectPaths, err := ProjectNamesToPaths(client, repo, []string{"Triage", "Roadmap"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedProjectPaths := []string{"ORG/1", "OWNER/REPO/2"}
	if !sliceEqual(projectPaths, expectedProjectPaths) {
		t.Errorf("expected projects paths %v, got %v", expectedProjectPaths, projectPaths)
	}
}

func Test_RepoResolveMetadataIDs(t *testing.T) {
	http := &httpmock.Registry{}
	client := NewClient(ReplaceTripper(http))

	repo, _ := ghrepo.FromFullName("OWNER/REPO")
	input := RepoResolveInput{
		Assignees: []string{"monalisa", "hubot"},
		Reviewers: []string{"monalisa", "octocat", "OWNER/core", "/robots"},
		Labels:    []string{"bug", "help wanted"},
	}

	expectedQuery := `query RepositoryResolveMetadataIDs {
u000: user(login:"monalisa"){id,login}
u001: user(login:"hubot"){id,login}
u002: user(login:"octocat"){id,login}
repository(owner:"OWNER",name:"REPO"){
l000: label(name:"bug"){id,name}
l001: label(name:"help wanted"){id,name}
}
organization(login:"OWNER"){
t000: team(slug:"core"){id,slug}
t001: team(slug:"robots"){id,slug}
}
}
`
	responseJSON := `
	{ "data": {
		"u000": { "login": "MonaLisa", "id": "MONAID" },
		"u001": { "login": "hubot", "id": "HUBOTID" },
		"u002": { "login": "octocat", "id": "OCTOID" },
		"repository": {
			"l000": { "name": "bug", "id": "BUGID" },
			"l001": { "name": "Help Wanted", "id": "HELPID" }
		},
		"organization": {
			"t000": { "slug": "core", "id": "COREID" },
			"t001": { "slug": "Robots", "id": "ROBOTID" }
		}
	} }
	`

	http.Register(
		httpmock.GraphQL(`query RepositoryResolveMetadataIDs\b`),
		httpmock.GraphQLQuery(responseJSON, func(q string, _ map[string]interface{}) {
			if q != expectedQuery {
				t.Errorf("expected query %q, got %q", expectedQuery, q)
			}
		}))

	result, err := RepoResolveMetadataIDs(client, repo, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedMemberIDs := []string{"MONAID", "HUBOTID", "OCTOID"}
	memberIDs, err := result.MembersToIDs([]string{"monalisa", "hubot", "octocat"})
	if err != nil {
		t.Errorf("error resolving members: %v", err)
	}
	if !sliceEqual(memberIDs, expectedMemberIDs) {
		t.Errorf("expected members %v, got %v", expectedMemberIDs, memberIDs)
	}

	expectedTeamIDs := []string{"COREID", "ROBOTID"}
	teamIDs, err := result.TeamsToIDs([]string{"/core", "/robots"})
	if err != nil {
		t.Errorf("error resolving teams: %v", err)
	}
	if !sliceEqual(teamIDs, expectedTeamIDs) {
		t.Errorf("expected members %v, got %v", expectedTeamIDs, teamIDs)
	}

	expectedLabelIDs := []string{"BUGID", "HELPID"}
	labelIDs, err := result.LabelsToIDs([]string{"bug", "help wanted"})
	if err != nil {
		t.Errorf("error resolving labels: %v", err)
	}
	if !sliceEqual(labelIDs, expectedLabelIDs) {
		t.Errorf("expected members %v, got %v", expectedLabelIDs, labelIDs)
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func Test_RepoMilestones(t *testing.T) {
	tests := []struct {
		state   string
		want    string
		wantErr bool
	}{
		{
			state: "open",
			want:  `"states":["OPEN"]`,
		},
		{
			state: "closed",
			want:  `"states":["CLOSED"]`,
		},
		{
			state: "all",
			want:  `"states":["OPEN","CLOSED"]`,
		},
		{
			state:   "invalid state",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		var query string
		reg := &httpmock.Registry{}
		reg.Register(httpmock.MatchAny, func(req *http.Request) (*http.Response, error) {
			buf := new(strings.Builder)
			_, err := io.Copy(buf, req.Body)
			if err != nil {
				return nil, err
			}
			query = buf.String()
			return httpmock.StringResponse("{}")(req)
		})
		client := NewClient(ReplaceTripper(reg))

		_, err := RepoMilestones(client, ghrepo.New("OWNER", "REPO"), tt.state)
		if (err != nil) != tt.wantErr {
			t.Errorf("RepoMilestones() error = %v, wantErr %v", err, tt.wantErr)
			return
		}
		if !strings.Contains(query, tt.want) {
			t.Errorf("query does not contain %v", tt.want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		assignee RepoAssignee
		want     string
	}{
		{
			name:     "assignee with name",
			assignee: RepoAssignee{"123", "octocat123", "Octavious Cath"},
			want:     "octocat123 (Octavious Cath)",
		},
		{
			name:     "assignee without name",
			assignee: RepoAssignee{"123", "octocat123", ""},
			want:     "octocat123",
		},
	}
	for _, tt := range tests {
		actual := tt.assignee.DisplayName()
		if actual != tt.want {
			t.Errorf("display name was %s wanted %s", actual, tt.want)
		}
	}
}
