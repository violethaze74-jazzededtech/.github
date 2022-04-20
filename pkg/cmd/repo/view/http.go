package view

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/internal/ghrepo"
)

var NotFoundError = errors.New("not found")

type RepoReadme struct {
	Filename string
	Content  string
	BaseURL  string
}

func RepositoryReadme(client *http.Client, repo ghrepo.Interface, branch string) (*RepoReadme, error) {
	apiClient := api.NewClientFromHTTP(client)
	var response struct {
		Name    string
		Content string
		HTMLURL string `json:"html_url"`
	}

	err := apiClient.REST(repo.RepoHost(), "GET", getReadmePath(repo, branch), nil, &response)
	if err != nil {
		var httpError api.HTTPError
		if errors.As(err, &httpError) && httpError.StatusCode == 404 {
			return nil, NotFoundError
		}
		return nil, err
	}

	decoded, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to decode readme: %w", err)
	}

	return &RepoReadme{
		Filename: response.Name,
		Content:  string(decoded),
		BaseURL:  response.HTMLURL,
	}, nil
}

func getReadmePath(repo ghrepo.Interface, branch string) string {
	path := fmt.Sprintf("repos/%s/readme", ghrepo.FullName(repo))
	if branch != "" {
		path = fmt.Sprintf("%s?ref=%s", path, branch)
	}
	return path
}
