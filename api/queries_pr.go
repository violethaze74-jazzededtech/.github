package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cli/cli/internal/ghinstance"
	"github.com/cli/cli/internal/ghrepo"
	"github.com/shurcooL/githubv4"
)

type PullRequestReviewState int

const (
	ReviewApprove PullRequestReviewState = iota
	ReviewRequestChanges
	ReviewComment
)

type PullRequestReviewInput struct {
	Body  string
	State PullRequestReviewState
}

type PullRequestsPayload struct {
	ViewerCreated   PullRequestAndTotalCount
	ReviewRequested PullRequestAndTotalCount
	CurrentPR       *PullRequest
	DefaultBranch   string
}

type PullRequestAndTotalCount struct {
	TotalCount   int
	PullRequests []PullRequest
}

type PullRequest struct {
	ID          string
	Number      int
	Title       string
	State       string
	Closed      bool
	URL         string
	BaseRefName string
	HeadRefName string
	Body        string
	Mergeable   string

	Author struct {
		Login string
	}
	HeadRepositoryOwner struct {
		Login string
	}
	HeadRepository struct {
		Name             string
		DefaultBranchRef struct {
			Name string
		}
	}
	IsCrossRepository   bool
	IsDraft             bool
	MaintainerCanModify bool

	ReviewDecision string

	Commits struct {
		TotalCount int
		Nodes      []struct {
			Commit struct {
				Oid               string
				StatusCheckRollup struct {
					Contexts struct {
						Nodes []struct {
							Name        string
							Context     string
							State       string
							Status      string
							Conclusion  string
							StartedAt   time.Time
							CompletedAt time.Time
							DetailsURL  string
							TargetURL   string
						}
					}
				}
			}
		}
	}
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				TypeName string `json:"__typename"`
				Login    string
				Name     string
			}
		}
		TotalCount int
	}
	Reviews struct {
		Nodes []struct {
			Author struct {
				Login string
			}
			State string
		}
	}
	Assignees struct {
		Nodes []struct {
			Login string
		}
		TotalCount int
	}
	Labels struct {
		Nodes []struct {
			Name string
		}
		TotalCount int
	}
	ProjectCards struct {
		Nodes []struct {
			Project struct {
				Name string
			}
			Column struct {
				Name string
			}
		}
		TotalCount int
	}
	Milestone struct {
		Title string
	}
}

type NotFoundError struct {
	error
}

func (err *NotFoundError) Unwrap() error {
	return err.error
}

func (pr PullRequest) HeadLabel() string {
	if pr.IsCrossRepository {
		return fmt.Sprintf("%s:%s", pr.HeadRepositoryOwner.Login, pr.HeadRefName)
	}
	return pr.HeadRefName
}

type PullRequestReviewStatus struct {
	ChangesRequested bool
	Approved         bool
	ReviewRequired   bool
}

type PullRequestMergeMethod int

const (
	PullRequestMergeMethodMerge PullRequestMergeMethod = iota
	PullRequestMergeMethodRebase
	PullRequestMergeMethodSquash
)

func (pr *PullRequest) ReviewStatus() PullRequestReviewStatus {
	var status PullRequestReviewStatus
	switch pr.ReviewDecision {
	case "CHANGES_REQUESTED":
		status.ChangesRequested = true
	case "APPROVED":
		status.Approved = true
	case "REVIEW_REQUIRED":
		status.ReviewRequired = true
	}
	return status
}

type PullRequestChecksStatus struct {
	Pending int
	Failing int
	Passing int
	Total   int
}

func (pr *PullRequest) ChecksStatus() (summary PullRequestChecksStatus) {
	if len(pr.Commits.Nodes) == 0 {
		return
	}
	commit := pr.Commits.Nodes[0].Commit
	for _, c := range commit.StatusCheckRollup.Contexts.Nodes {
		state := c.State // StatusContext
		if state == "" {
			// CheckRun
			if c.Status == "COMPLETED" {
				state = c.Conclusion
			} else {
				state = c.Status
			}
		}
		switch state {
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			summary.Passing++
		case "ERROR", "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
			summary.Failing++
		case "EXPECTED", "REQUESTED", "QUEUED", "PENDING", "IN_PROGRESS", "STALE":
			summary.Pending++
		default:
			panic(fmt.Errorf("unsupported status: %q", state))
		}
		summary.Total++
	}
	return
}

func (c Client) PullRequestDiff(baseRepo ghrepo.Interface, prNumber int) (io.ReadCloser, error) {
	url := fmt.Sprintf("%srepos/%s/pulls/%d",
		ghinstance.RESTPrefix(baseRepo.RepoHost()), ghrepo.FullName(baseRepo), prNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github.v3.diff; charset=utf-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 404 {
		return nil, &NotFoundError{errors.New("pull request not found")}
	} else if resp.StatusCode != 200 {
		return nil, HandleHTTPError(resp)
	}

	return resp.Body, nil
}

type pullRequestFeature struct {
	HasReviewDecision    bool
	HasStatusCheckRollup bool
}

func determinePullRequestFeatures(httpClient *http.Client, hostname string) (prFeatures pullRequestFeature, err error) {
	if !ghinstance.IsEnterprise(hostname) {
		prFeatures.HasReviewDecision = true
		prFeatures.HasStatusCheckRollup = true
		return
	}

	var featureDetection struct {
		PullRequest struct {
			Fields []struct {
				Name string
			} `graphql:"fields(includeDeprecated: true)"`
		} `graphql:"PullRequest: __type(name: \"PullRequest\")"`
		Commit struct {
			Fields []struct {
				Name string
			} `graphql:"fields(includeDeprecated: true)"`
		} `graphql:"Commit: __type(name: \"Commit\")"`
	}

	v4 := graphQLClient(httpClient, hostname)
	err = v4.QueryNamed(context.Background(), "PullRequest_fields", &featureDetection, nil)
	if err != nil {
		return
	}

	for _, field := range featureDetection.PullRequest.Fields {
		switch field.Name {
		case "reviewDecision":
			prFeatures.HasReviewDecision = true
		}
	}
	for _, field := range featureDetection.Commit.Fields {
		switch field.Name {
		case "statusCheckRollup":
			prFeatures.HasStatusCheckRollup = true
		}
	}
	return
}

func PullRequests(client *Client, repo ghrepo.Interface, currentPRNumber int, currentPRHeadRef, currentUsername string) (*PullRequestsPayload, error) {
	type edges struct {
		TotalCount int
		Edges      []struct {
			Node PullRequest
		}
	}

	type response struct {
		Repository struct {
			DefaultBranchRef struct {
				Name string
			}
			PullRequests edges
			PullRequest  *PullRequest
		}
		ViewerCreated   edges
		ReviewRequested edges
	}

	cachedClient := makeCachedClient(client.http, time.Hour*24)
	prFeatures, err := determinePullRequestFeatures(cachedClient, repo.RepoHost())
	if err != nil {
		return nil, err
	}

	var reviewsFragment string
	if prFeatures.HasReviewDecision {
		reviewsFragment = "reviewDecision"
	}

	var statusesFragment string
	if prFeatures.HasStatusCheckRollup {
		statusesFragment = `
		commits(last: 1) {
			nodes {
				commit {
					statusCheckRollup {
						contexts(last: 100) {
							nodes {
								...on StatusContext {
									state
								}
								...on CheckRun {
									conclusion
									status
								}
							}
						}
					}
				}
			}
		}
		`
	}

	fragments := fmt.Sprintf(`
	fragment pr on PullRequest {
		number
		title
		state
		url
		headRefName
		headRepositoryOwner {
			login
		}
		isCrossRepository
		isDraft
		%s
	}
	fragment prWithReviews on PullRequest {
		...pr
		%s
	}
	`, statusesFragment, reviewsFragment)

	queryPrefix := `
	query PullRequestStatus($owner: String!, $repo: String!, $headRefName: String!, $viewerQuery: String!, $reviewerQuery: String!, $per_page: Int = 10) {
		repository(owner: $owner, name: $repo) {
			defaultBranchRef { name }
			pullRequests(headRefName: $headRefName, first: $per_page, orderBy: { field: CREATED_AT, direction: DESC }) {
				totalCount
				edges {
					node {
						...prWithReviews
					}
				}
			}
		}
	`
	if currentPRNumber > 0 {
		queryPrefix = `
		query PullRequestStatus($owner: String!, $repo: String!, $number: Int!, $viewerQuery: String!, $reviewerQuery: String!, $per_page: Int = 10) {
			repository(owner: $owner, name: $repo) {
				defaultBranchRef { name }
				pullRequest(number: $number) {
					...prWithReviews
				}
			}
		`
	}

	query := fragments + queryPrefix + `
      viewerCreated: search(query: $viewerQuery, type: ISSUE, first: $per_page) {
       totalCount: issueCount
        edges {
          node {
            ...prWithReviews
          }
        }
      }
      reviewRequested: search(query: $reviewerQuery, type: ISSUE, first: $per_page) {
        totalCount: issueCount
        edges {
          node {
            ...pr
          }
        }
      }
    }
	`

	if currentUsername == "@me" && ghinstance.IsEnterprise(repo.RepoHost()) {
		currentUsername, err = CurrentLoginName(client, repo.RepoHost())
		if err != nil {
			return nil, err
		}
	}

	viewerQuery := fmt.Sprintf("repo:%s state:open is:pr author:%s", ghrepo.FullName(repo), currentUsername)
	reviewerQuery := fmt.Sprintf("repo:%s state:open review-requested:%s", ghrepo.FullName(repo), currentUsername)

	branchWithoutOwner := currentPRHeadRef
	if idx := strings.Index(currentPRHeadRef, ":"); idx >= 0 {
		branchWithoutOwner = currentPRHeadRef[idx+1:]
	}

	variables := map[string]interface{}{
		"viewerQuery":   viewerQuery,
		"reviewerQuery": reviewerQuery,
		"owner":         repo.RepoOwner(),
		"repo":          repo.RepoName(),
		"headRefName":   branchWithoutOwner,
		"number":        currentPRNumber,
	}

	var resp response
	err = client.GraphQL(repo.RepoHost(), query, variables, &resp)
	if err != nil {
		return nil, err
	}

	var viewerCreated []PullRequest
	for _, edge := range resp.ViewerCreated.Edges {
		viewerCreated = append(viewerCreated, edge.Node)
	}

	var reviewRequested []PullRequest
	for _, edge := range resp.ReviewRequested.Edges {
		reviewRequested = append(reviewRequested, edge.Node)
	}

	var currentPR = resp.Repository.PullRequest
	if currentPR == nil {
		for _, edge := range resp.Repository.PullRequests.Edges {
			if edge.Node.HeadLabel() == currentPRHeadRef {
				currentPR = &edge.Node
				break // Take the most recent PR for the current branch
			}
		}
	}

	payload := PullRequestsPayload{
		ViewerCreated: PullRequestAndTotalCount{
			PullRequests: viewerCreated,
			TotalCount:   resp.ViewerCreated.TotalCount,
		},
		ReviewRequested: PullRequestAndTotalCount{
			PullRequests: reviewRequested,
			TotalCount:   resp.ReviewRequested.TotalCount,
		},
		CurrentPR:     currentPR,
		DefaultBranch: resp.Repository.DefaultBranchRef.Name,
	}

	return &payload, nil
}

func prCommitsFragment(httpClient *http.Client, hostname string) (string, error) {
	cachedClient := makeCachedClient(httpClient, time.Hour*24)
	if prFeatures, err := determinePullRequestFeatures(cachedClient, hostname); err != nil {
		return "", err
	} else if !prFeatures.HasStatusCheckRollup {
		return "", nil
	}

	return `
	commits(last: 1) {
		totalCount
		nodes {
			commit {
				oid
				statusCheckRollup {
					contexts(last: 100) {
						nodes {
							...on StatusContext {
								context
								state
								targetUrl
							}
							...on CheckRun {
								name
								status
								conclusion
								startedAt
								completedAt
								detailsUrl
							}
						}
					}
				}
			}
		}
	}
	`, nil
}

func PullRequestByNumber(client *Client, repo ghrepo.Interface, number int) (*PullRequest, error) {
	type response struct {
		Repository struct {
			PullRequest PullRequest
		}
	}

	statusesFragment, err := prCommitsFragment(client.http, repo.RepoHost())
	if err != nil {
		return nil, err
	}

	query := `
	query PullRequestByNumber($owner: String!, $repo: String!, $pr_number: Int!) {
		repository(owner: $owner, name: $repo) {
			pullRequest(number: $pr_number) {
				id
				url
				number
				title
				state
				closed
				body
				mergeable
				author {
				  login
				}
				` + statusesFragment + `
				baseRefName
				headRefName
				headRepositoryOwner {
					login
				}
				headRepository {
					name
				}
				isCrossRepository
				isDraft
				maintainerCanModify
				reviewRequests(first: 100) {
					nodes {
						requestedReviewer {
							__typename
							...on User {
								login
							}
							...on Team {
								name
							}
						}
					}
					totalCount
				}
				reviews(last: 100) {
					nodes {
						author {
						  login
						}
						state
					}
					totalCount
				}
				assignees(first: 100) {
					nodes {
						login
					}
					totalCount
				}
				labels(first: 100) {
					nodes {
						name
					}
					totalCount
				}
				projectCards(first: 100) {
					nodes {
						project {
							name
						}
						column {
							name
						}
					}
					totalCount
				}
				milestone{
					title
				}
			}
		}
	}`

	variables := map[string]interface{}{
		"owner":     repo.RepoOwner(),
		"repo":      repo.RepoName(),
		"pr_number": number,
	}

	var resp response
	err = client.GraphQL(repo.RepoHost(), query, variables, &resp)
	if err != nil {
		return nil, err
	}

	return &resp.Repository.PullRequest, nil
}

func PullRequestForBranch(client *Client, repo ghrepo.Interface, baseBranch, headBranch string, stateFilters []string) (*PullRequest, error) {
	type response struct {
		Repository struct {
			PullRequests struct {
				Nodes []PullRequest
			}
		}
	}

	statusesFragment, err := prCommitsFragment(client.http, repo.RepoHost())
	if err != nil {
		return nil, err
	}

	query := `
	query PullRequestForBranch($owner: String!, $repo: String!, $headRefName: String!, $states: [PullRequestState!]) {
		repository(owner: $owner, name: $repo) {
			pullRequests(headRefName: $headRefName, states: $states, first: 30, orderBy: { field: CREATED_AT, direction: DESC }) {
				nodes {
					id
					number
					title
					state
					body
					mergeable
					author {
						login
					}
					` + statusesFragment + `
					url
					baseRefName
					headRefName
					headRepositoryOwner {
						login
					}
					headRepository {
						name
					}
					isCrossRepository
					isDraft
					maintainerCanModify
					reviewRequests(first: 100) {
						nodes {
							requestedReviewer {
								__typename
								...on User {
									login
								}
								...on Team {
									name
								}
							}
						}
						totalCount
					}
					reviews(last: 100) {
						nodes {
							author {
							  login
							}
							state
						}
						totalCount
					}
					assignees(first: 100) {
						nodes {
							login
						}
						totalCount
					}
					labels(first: 100) {
						nodes {
							name
						}
						totalCount
					}
					projectCards(first: 100) {
						nodes {
							project {
								name
							}
							column {
								name
							}
						}
						totalCount
					}
					milestone{
						title
					}
				}
			}
		}
	}`

	branchWithoutOwner := headBranch
	if idx := strings.Index(headBranch, ":"); idx >= 0 {
		branchWithoutOwner = headBranch[idx+1:]
	}

	variables := map[string]interface{}{
		"owner":       repo.RepoOwner(),
		"repo":        repo.RepoName(),
		"headRefName": branchWithoutOwner,
		"states":      stateFilters,
	}

	var resp response
	err = client.GraphQL(repo.RepoHost(), query, variables, &resp)
	if err != nil {
		return nil, err
	}

	prs := resp.Repository.PullRequests.Nodes
	sortPullRequestsByState(prs)

	for _, pr := range prs {
		if pr.HeadLabel() == headBranch && (baseBranch == "" || pr.BaseRefName == baseBranch) {
			return &pr, nil
		}
	}

	return nil, &NotFoundError{fmt.Errorf("no pull requests found for branch %q", headBranch)}
}

// sortPullRequestsByState sorts a PullRequest slice by open-first
func sortPullRequestsByState(prs []PullRequest) {
	sort.SliceStable(prs, func(a, b int) bool {
		return prs[a].State == "OPEN"
	})
}

// CreatePullRequest creates a pull request in a GitHub repository
func CreatePullRequest(client *Client, repo *Repository, params map[string]interface{}) (*PullRequest, error) {
	query := `
		mutation PullRequestCreate($input: CreatePullRequestInput!) {
			createPullRequest(input: $input) {
				pullRequest {
					id
					url
				}
			}
	}`

	inputParams := map[string]interface{}{
		"repositoryId": repo.ID,
	}
	for key, val := range params {
		switch key {
		case "title", "body", "draft", "baseRefName", "headRefName":
			inputParams[key] = val
		}
	}
	variables := map[string]interface{}{
		"input": inputParams,
	}

	result := struct {
		CreatePullRequest struct {
			PullRequest PullRequest
		}
	}{}

	err := client.GraphQL(repo.RepoHost(), query, variables, &result)
	if err != nil {
		return nil, err
	}
	pr := &result.CreatePullRequest.PullRequest

	// metadata parameters aren't currently available in `createPullRequest`,
	// but they are in `updatePullRequest`
	updateParams := make(map[string]interface{})
	for key, val := range params {
		switch key {
		case "assigneeIds", "labelIds", "projectIds", "milestoneId":
			if !isBlank(val) {
				updateParams[key] = val
			}
		}
	}
	if len(updateParams) > 0 {
		updateQuery := `
		mutation PullRequestCreateMetadata($input: UpdatePullRequestInput!) {
			updatePullRequest(input: $input) { clientMutationId }
		}`
		updateParams["pullRequestId"] = pr.ID
		variables := map[string]interface{}{
			"input": updateParams,
		}
		err := client.GraphQL(repo.RepoHost(), updateQuery, variables, &result)
		if err != nil {
			return pr, err
		}
	}

	// reviewers are requested in yet another additional mutation
	reviewParams := make(map[string]interface{})
	if ids, ok := params["userReviewerIds"]; ok && !isBlank(ids) {
		reviewParams["userIds"] = ids
	}
	if ids, ok := params["teamReviewerIds"]; ok && !isBlank(ids) {
		reviewParams["teamIds"] = ids
	}

	if len(reviewParams) > 0 {
		reviewQuery := `
		mutation PullRequestCreateRequestReviews($input: RequestReviewsInput!) {
			requestReviews(input: $input) { clientMutationId }
		}`
		reviewParams["pullRequestId"] = pr.ID
		reviewParams["union"] = true
		variables := map[string]interface{}{
			"input": reviewParams,
		}
		err := client.GraphQL(repo.RepoHost(), reviewQuery, variables, &result)
		if err != nil {
			return pr, err
		}
	}

	return pr, nil
}

func isBlank(v interface{}) bool {
	switch vv := v.(type) {
	case string:
		return vv == ""
	case []string:
		return len(vv) == 0
	default:
		return true
	}
}

func AddReview(client *Client, repo ghrepo.Interface, pr *PullRequest, input *PullRequestReviewInput) error {
	var mutation struct {
		AddPullRequestReview struct {
			ClientMutationID string
		} `graphql:"addPullRequestReview(input:$input)"`
	}

	state := githubv4.PullRequestReviewEventComment
	switch input.State {
	case ReviewApprove:
		state = githubv4.PullRequestReviewEventApprove
	case ReviewRequestChanges:
		state = githubv4.PullRequestReviewEventRequestChanges
	}

	body := githubv4.String(input.Body)
	variables := map[string]interface{}{
		"input": githubv4.AddPullRequestReviewInput{
			PullRequestID: pr.ID,
			Event:         &state,
			Body:          &body,
		},
	}

	gql := graphQLClient(client.http, repo.RepoHost())
	return gql.MutateNamed(context.Background(), "PullRequestReviewAdd", &mutation, variables)
}

func PullRequestList(client *Client, repo ghrepo.Interface, vars map[string]interface{}, limit int) (*PullRequestAndTotalCount, error) {
	type prBlock struct {
		Edges []struct {
			Node PullRequest
		}
		PageInfo struct {
			HasNextPage bool
			EndCursor   string
		}
		TotalCount int
		IssueCount int
	}
	type response struct {
		Repository struct {
			PullRequests prBlock
		}
		Search prBlock
	}

	fragment := `
	fragment pr on PullRequest {
		number
		title
		state
		url
		headRefName
		headRepositoryOwner {
			login
		}
		isCrossRepository
		isDraft
	}
	`

	// If assignee wasn't specified, use `Repository.pullRequest` for ability to
	// query by multiple labels
	query := fragment + `
    query PullRequestList(
		$owner: String!,
		$repo: String!,
		$limit: Int!,
		$endCursor: String,
		$baseBranch: String,
		$labels: [String!],
		$state: [PullRequestState!] = OPEN
	) {
      repository(owner: $owner, name: $repo) {
        pullRequests(
			states: $state,
			baseRefName: $baseBranch,
			labels: $labels,
			first: $limit,
			after: $endCursor,
			orderBy: {field: CREATED_AT, direction: DESC}
			) {
				totalCount
				edges {
					node {
						...pr
					}
				}
				pageInfo {
					hasNextPage
					endCursor
				}
			}
		}
	}`

	var check = make(map[int]struct{})
	var prs []PullRequest
	pageLimit := min(limit, 100)
	variables := map[string]interface{}{}
	res := PullRequestAndTotalCount{}

	// If assignee was specified, use the `search` API rather than
	// `Repository.pullRequests`, but this mode doesn't support multiple labels
	if assignee, ok := vars["assignee"].(string); ok {
		query = fragment + `
		query PullRequestList(
			$q: String!,
			$limit: Int!,
			$endCursor: String,
		) {
			search(query: $q, type: ISSUE, first: $limit, after: $endCursor) {
				issueCount
				edges {
					node {
						...pr
					}
				}
				pageInfo {
					hasNextPage
					endCursor
				}
			}
		}`
		search := []string{
			fmt.Sprintf("repo:%s/%s", repo.RepoOwner(), repo.RepoName()),
			fmt.Sprintf("assignee:%s", assignee),
			"is:pr",
			"sort:created-desc",
		}
		if states, ok := vars["state"].([]string); ok && len(states) == 1 {
			switch states[0] {
			case "OPEN":
				search = append(search, "state:open")
			case "CLOSED":
				search = append(search, "state:closed")
			case "MERGED":
				search = append(search, "is:merged")
			}
		}
		if labels, ok := vars["labels"].([]string); ok && len(labels) > 0 {
			if len(labels) > 1 {
				return nil, fmt.Errorf("multiple labels with --assignee are not supported")
			}
			search = append(search, fmt.Sprintf(`label:"%s"`, labels[0]))
		}
		if baseBranch, ok := vars["baseBranch"].(string); ok {
			search = append(search, fmt.Sprintf(`base:"%s"`, baseBranch))
		}
		variables["q"] = strings.Join(search, " ")
	} else {
		variables["owner"] = repo.RepoOwner()
		variables["repo"] = repo.RepoName()
		for name, val := range vars {
			variables[name] = val
		}
	}
loop:
	for {
		variables["limit"] = pageLimit
		var data response
		err := client.GraphQL(repo.RepoHost(), query, variables, &data)
		if err != nil {
			return nil, err
		}
		prData := data.Repository.PullRequests
		res.TotalCount = prData.TotalCount
		if _, ok := variables["q"]; ok {
			prData = data.Search
			res.TotalCount = prData.IssueCount
		}

		for _, edge := range prData.Edges {
			if _, exists := check[edge.Node.Number]; exists {
				continue
			}

			prs = append(prs, edge.Node)
			check[edge.Node.Number] = struct{}{}
			if len(prs) == limit {
				break loop
			}
		}

		if prData.PageInfo.HasNextPage {
			variables["endCursor"] = prData.PageInfo.EndCursor
			pageLimit = min(pageLimit, limit-len(prs))
		} else {
			break
		}
	}
	res.PullRequests = prs
	return &res, nil
}

func PullRequestClose(client *Client, repo ghrepo.Interface, pr *PullRequest) error {
	var mutation struct {
		ClosePullRequest struct {
			PullRequest struct {
				ID githubv4.ID
			}
		} `graphql:"closePullRequest(input: $input)"`
	}

	variables := map[string]interface{}{
		"input": githubv4.ClosePullRequestInput{
			PullRequestID: pr.ID,
		},
	}

	gql := graphQLClient(client.http, repo.RepoHost())
	err := gql.MutateNamed(context.Background(), "PullRequestClose", &mutation, variables)

	return err
}

func PullRequestReopen(client *Client, repo ghrepo.Interface, pr *PullRequest) error {
	var mutation struct {
		ReopenPullRequest struct {
			PullRequest struct {
				ID githubv4.ID
			}
		} `graphql:"reopenPullRequest(input: $input)"`
	}

	variables := map[string]interface{}{
		"input": githubv4.ReopenPullRequestInput{
			PullRequestID: pr.ID,
		},
	}

	gql := graphQLClient(client.http, repo.RepoHost())
	err := gql.MutateNamed(context.Background(), "PullRequestReopen", &mutation, variables)

	return err
}

func PullRequestMerge(client *Client, repo ghrepo.Interface, pr *PullRequest, m PullRequestMergeMethod) error {
	mergeMethod := githubv4.PullRequestMergeMethodMerge
	switch m {
	case PullRequestMergeMethodRebase:
		mergeMethod = githubv4.PullRequestMergeMethodRebase
	case PullRequestMergeMethodSquash:
		mergeMethod = githubv4.PullRequestMergeMethodSquash
	}

	var mutation struct {
		MergePullRequest struct {
			PullRequest struct {
				ID githubv4.ID
			}
		} `graphql:"mergePullRequest(input: $input)"`
	}

	input := githubv4.MergePullRequestInput{
		PullRequestID: pr.ID,
		MergeMethod:   &mergeMethod,
	}

	if m == PullRequestMergeMethodSquash {
		commitHeadline := githubv4.String(fmt.Sprintf("%s (#%d)", pr.Title, pr.Number))
		input.CommitHeadline = &commitHeadline
	}

	variables := map[string]interface{}{
		"input": input,
	}

	gql := graphQLClient(client.http, repo.RepoHost())
	err := gql.MutateNamed(context.Background(), "PullRequestMerge", &mutation, variables)

	return err
}

func PullRequestReady(client *Client, repo ghrepo.Interface, pr *PullRequest) error {
	var mutation struct {
		MarkPullRequestReadyForReview struct {
			PullRequest struct {
				ID githubv4.ID
			}
		} `graphql:"markPullRequestReadyForReview(input: $input)"`
	}

	variables := map[string]interface{}{
		"input": githubv4.MarkPullRequestReadyForReviewInput{
			PullRequestID: pr.ID,
		},
	}

	gql := graphQLClient(client.http, repo.RepoHost())
	return gql.MutateNamed(context.Background(), "PullRequestReadyForReview", &mutation, variables)
}

func BranchDeleteRemote(client *Client, repo ghrepo.Interface, branch string) error {
	path := fmt.Sprintf("repos/%s/%s/git/refs/heads/%s", repo.RepoOwner(), repo.RepoName(), branch)
	return client.REST(repo.RepoHost(), "DELETE", path, nil, nil)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
