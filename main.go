package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

type PullRequest struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	MergedAt    string `json:"mergedAt"`
	HeadRefName string `json:"headRefName"`
}

type ghHostConfig struct {
	OAuthToken string `yaml:"oauth_token"`
}

func getGitHubToken() (string, error) {
	// Check environment variable first (GH_TOKEN or GITHUB_TOKEN)
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token, nil
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// Find gh config directory
	configDir := os.Getenv("GH_CONFIG_DIR")
	if configDir == "" {
		if runtime.GOOS == "windows" {
			configDir = filepath.Join(os.Getenv("APPDATA"), "GitHub CLI")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("could not find home directory: %w", err)
			}
			configDir = filepath.Join(home, ".config", "gh")
		}
	}

	hostsFile := filepath.Join(configDir, "hosts.yml")
	data, err := os.ReadFile(hostsFile)
	if err != nil {
		return "", fmt.Errorf("could not read gh config file %s: %w", hostsFile, err)
	}

	var hosts map[string]ghHostConfig
	if err := yaml.Unmarshal(data, &hosts); err != nil {
		return "", fmt.Errorf("could not parse gh config file: %w", err)
	}

	// Check for oauth_token in hosts.yml (older gh versions)
	if hostConfig, ok := hosts["github.com"]; ok && hostConfig.OAuthToken != "" {
		return hostConfig.OAuthToken, nil
	}

	// Try system keyring (newer gh versions store token here)
	if token, err := keyring.Get("gh:github.com", ""); err == nil && token != "" {
		return token, nil
	}

	return "", fmt.Errorf("no GitHub token found in gh config or keyring")
}

func main() {
	dryRun := false
	for _, arg := range os.Args[1:] {
		if arg == "--dry-run" || arg == "-n" {
			dryRun = true
		}
		if arg == "--help" || arg == "-h" {
			fmt.Println("Usage: git-tidy [--dry-run|-n]")
			fmt.Println()
			fmt.Println("Deletes local branches whose PRs have been merged.")
			fmt.Println()
			fmt.Println("Options:")
			fmt.Println("  --dry-run, -n  Show what would be deleted without deleting")
			os.Exit(0)
		}
	}

	branches, err := getLocalBranches()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting local branches: %v\n", err)
		os.Exit(1)
	}

	if len(branches) == 0 {
		fmt.Println("No branches to check (only main exists)")
		return
	}

	fmt.Printf("Found %d branches to check\n", len(branches))

	token, err := getGitHubToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting GitHub token: %v\n", err)
		os.Exit(1)
	}

	repo, err := getRepoName()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting repo name: %v\n", err)
		os.Exit(1)
	}

	var toDelete []string
	for _, branch := range branches {
		pr, err := findPRForBranch(repo, branch, token)
		if err != nil {
			fmt.Printf("  %s: error checking PR: %v\n", branch, err)
			continue
		}
		if pr == nil {
			fmt.Printf("  %s: no PR found\n", branch)
			continue
		}
		if pr.MergedAt != "" {
			fmt.Printf("  %s: PR #%d merged\n", branch, pr.Number)
			toDelete = append(toDelete, branch)
		} else {
			fmt.Printf("  %s: PR #%d not merged (state: %s)\n", branch, pr.Number, pr.State)
		}
	}

	if len(toDelete) == 0 {
		fmt.Println("\nNo branches to delete")
		return
	}

	fmt.Printf("\nBranches to delete: %d\n", len(toDelete))
	for _, branch := range toDelete {
		if dryRun {
			fmt.Printf("  Would delete: %s\n", branch)
		} else {
			err := deleteBranch(branch)
			if err != nil {
				fmt.Printf("  Error deleting %s: %v\n", branch, err)
			} else {
				fmt.Printf("  Deleted: %s\n", branch)
			}
		}
	}
}

func getLocalBranches() ([]string, error) {
	cmd := exec.Command("git", "branch", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		branch := strings.TrimSpace(scanner.Text())
		if branch != "" && branch != "main" && branch != "master" {
			branches = append(branches, branch)
		}
	}
	return branches, scanner.Err()
}

func getRepoName() (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL: %w", err)
	}

	remoteURL := strings.TrimSpace(string(output))
	return parseGitHubRepo(remoteURL)
}

func parseGitHubRepo(remoteURL string) (string, error) {
	// Handle SSH URLs: git@github.com:owner/repo.git
	sshPattern := regexp.MustCompile(`git@github\.com:([^/]+)/(.+?)(?:\.git)?$`)
	if matches := sshPattern.FindStringSubmatch(remoteURL); matches != nil {
		return matches[1] + "/" + strings.TrimSuffix(matches[2], ".git"), nil
	}

	// Handle HTTPS URLs: https://github.com/owner/repo.git
	if u, err := url.Parse(remoteURL); err == nil && u.Host == "github.com" {
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		return path, nil
	}

	return "", fmt.Errorf("could not parse GitHub repo from URL: %s", remoteURL)
}

func findPRForBranch(repo, branch, token string) (*PullRequest, error) {
	// GitHub API: GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=all
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", repo)
	}
	owner := parts[0]

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/pulls?head=%s:%s&state=all&per_page=1",
		repo, owner, branch)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var prs []struct {
		Number    int    `json:"number"`
		State     string `json:"state"`
		MergedAt  string `json:"merged_at"`
		Head      struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}

	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, err
	}

	if len(prs) == 0 {
		return nil, nil
	}

	return &PullRequest{
		Number:      prs[0].Number,
		State:       prs[0].State,
		MergedAt:    prs[0].MergedAt,
		HeadRefName: prs[0].Head.Ref,
	}, nil
}

func deleteBranch(branch string) error {
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
