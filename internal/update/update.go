package update

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

// Version is set at build time via -ldflags "-X github.com/SeanoChang/keel/internal/update.Version=<tag>"
var Version = "dev"

const githubRepo = "SeanoChang/keel"

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Result describes what happened during an update.
type Result struct {
	CurrentVersion string
	NewVersion     string
	AlreadyCurrent bool
}

// Run checks for the latest GitHub release and replaces the current binary
// if a newer version is available. onProgress is called with status messages.
func Run(currentVersion string, onProgress func(string)) (*Result, error) {
	onProgress(fmt.Sprintf("Current version: %s. Checking for updates...", currentVersion))

	rel, err := FetchLatestRelease()
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}

	if rel.TagName == currentVersion {
		return &Result{
			CurrentVersion: currentVersion,
			NewVersion:     currentVersion,
			AlreadyCurrent: true,
		}, nil
	}

	onProgress(fmt.Sprintf("New version available: %s → %s", currentVersion, rel.TagName))

	assetName := PlatformAsset()
	if assetName == "" {
		return nil, fmt.Errorf("unsupported platform: %s/%s — download manually from https://github.com/%s/releases", runtime.GOOS, runtime.GOARCH, githubRepo)
	}

	var downloadURL string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return nil, fmt.Errorf("asset %q not found in release %s", assetName, rel.TagName)
	}

	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	onProgress(fmt.Sprintf("Downloading %s...", assetName))
	if err := DownloadAndReplace(downloadURL, exePath); err != nil {
		return nil, fmt.Errorf("replace binary: %w", err)
	}

	return &Result{
		CurrentVersion: currentVersion,
		NewVersion:     rel.TagName,
	}, nil
}

// FetchLatestRelease queries GitHub for the latest release.
func FetchLatestRelease() (*Release, error) {
	url := "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "keel/update")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// PlatformAsset returns the expected binary asset name for the current OS/arch.
func PlatformAsset() string {
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "keel-darwin-arm64"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "keel-linux-amd64"
	default:
		return ""
	}
}

// DownloadAndReplace downloads a binary from url and atomically replaces dest.
func DownloadAndReplace(url, dest string) error {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	// Write to a sibling temp file so os.Rename is atomic (same filesystem).
	tmp := dest + ".keel-update"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write download: %w", err)
	}
	f.Close()

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	log.Printf("[update] replaced %s", dest)
	return nil
}
