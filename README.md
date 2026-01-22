# git-tidy

A command-line utility that automatically cleans up local Git branches whose pull requests have been merged on GitHub.

## Rationale

Over time, developers accumulate local Git branches from feature development and bug fixes. After a pull request is merged into main, the local branch becomes obsolete but remains in the repository, cluttering the branch list. Running `git branch` might show dozens of stale branches that no longer serve any purpose.

git-tidy solves this problem by:

1. Scanning all local branches (excluding main/master)
2. Checking the status of associated pull requests on GitHub via the API
3. Identifying which branches have been merged
4. Deleting those branches to keep the repository tidy

This saves developers the manual effort of tracking which branches are safe to delete.

## Building

git-tidy is written in Go. You need Go 1.21 or later.

```bash
# Clone the repository
git clone https://github.com/yourusername/git-tidy.git
cd git-tidy

# Build the binary
go build -o git-tidy main.go

# Optionally install to your PATH
go install
```

To tidy up dependencies:

```bash
go mod tidy
```

## Configuring Credentials

git-tidy needs a GitHub token to query the GitHub API for pull request status. It automatically retrieves your token from the GitHub CLI (`gh`) configuration, so you don't need to manage tokens manually.

### Using the GitHub CLI (Recommended)

The easiest way to set up credentials is to authenticate with the GitHub CLI:

```bash
# Install gh if you haven't already
# macOS: brew install gh
# Ubuntu: sudo apt install gh
# See https://cli.github.com for other platforms

# Authenticate with GitHub
gh auth login
```

Follow the prompts to authenticate. Once complete, git-tidy will automatically use the same token.

### Token Retrieval Priority

git-tidy looks for a GitHub token in the following order:

1. **Environment variables**: `GH_TOKEN` or `GITHUB_TOKEN`
2. **gh config file**: `~/.config/gh/hosts.yml` (or `%APPDATA%\GitHub CLI\hosts.yml` on Windows)
3. **System keyring**: Modern versions of `gh` store tokens in the OS keyring for better security

If you prefer to use an environment variable:

```bash
export GH_TOKEN=ghp_your_token_here
```

### Required Token Permissions

The token needs `repo` scope to read pull request information from private repositories, or just public access for public repositories.

## Usage

Run git-tidy from within a Git repository:

```bash
# Preview what would be deleted (recommended first step)
git-tidy --dry-run

# Actually delete merged branches
git-tidy
```

### Options

| Option | Short | Description |
|--------|-------|-------------|
| `--dry-run` | `-n` | Preview what would be deleted without actually deleting |
| `--help` | `-h` | Display usage information |

### Example Output

```
$ git-tidy --dry-run
Found 5 local branches (excluding main/master)

  feature/add-login         -> PR #42 merged (would delete)
  bugfix/fix-typo           -> PR #38 merged (would delete)
  feature/new-dashboard     -> PR #45 not merged (state: open)
  experiment/test-idea      -> no PR found
  feature/old-feature       -> PR #30 merged (would delete)

Dry run complete. Would delete 3 branches.
```

Running without `--dry-run` will actually delete the merged branches:

```
$ git-tidy
Found 5 local branches (excluding main/master)

  feature/add-login         -> PR #42 merged, deleting...
  bugfix/fix-typo           -> PR #38 merged, deleting...
  feature/new-dashboard     -> PR #45 not merged (state: open)
  experiment/test-idea      -> no PR found
  feature/old-feature       -> PR #30 merged, deleting...

Deleted 3 branches.
```

## How It Works

1. **Branch Discovery**: Runs `git branch --format=%(refname:short)` to get all local branches, excluding main and master
2. **Repository Identification**: Extracts the owner/repo from the Git remote URL (supports both SSH and HTTPS)
3. **PR Status Check**: For each branch, queries the GitHub API to find associated pull requests
4. **Merge Detection**: Checks if the PR has a non-empty `merged_at` timestamp
5. **Cleanup**: Deletes merged branches using `git branch -D`

## Platform Support

git-tidy works on:

- Linux
- macOS
- Windows

Credential retrieval adapts automatically to each platform's conventions.

## License

MIT
