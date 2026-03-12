package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/SeanoChang/keel/internal/migrate"
)

// Version is set at build time via -ldflags "-X github.com/SeanoChang/keel/cmd.Version=<tag>"
var Version = "dev"

const githubRepo = "SeanoChang/keel"

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update keel to the latest GitHub release and migrate workspaces",
	RunE:  runUpdate,
}

var updateMigrateOnly bool

func runUpdate(cmd *cobra.Command, args []string) error {
	if !updateMigrateOnly {
		if err := selfUpdate(); err != nil {
			return err
		}
	}

	fmt.Println("Running workspace migrations...")
	if err := migrate.ScanAndMigrate(""); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	fmt.Println("Migrations complete.")
	return nil
}

func selfUpdate() error {
	fmt.Printf("Current version: %s\n", Version)
	fmt.Println("Checking for updates...")

	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}

	if rel.TagName == Version {
		fmt.Printf("Already on latest version (%s).\n", Version)
		return nil
	}

	fmt.Printf("New version available: %s → %s\n", Version, rel.TagName)

	assetName := platformAsset()
	if assetName == "" {
		return fmt.Errorf("unsupported platform: %s/%s — download manually from https://github.com/%s/releases", runtime.GOOS, runtime.GOARCH, githubRepo)
	}

	var downloadURL string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("asset %q not found in release %s", assetName, rel.TagName)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	fmt.Printf("Downloading %s...\n", assetName)
	if err := downloadAndReplace(downloadURL, exePath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	fmt.Printf("Updated to %s. Re-run keel to use the new version.\n", rel.TagName)
	return nil
}

func fetchLatestRelease() (*githubRelease, error) {
	url := "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "keel/"+Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func platformAsset() string {
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "keel-darwin-arm64"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "keel-linux-amd64"
	default:
		return ""
	}
}

func downloadAndReplace(url, dest string) error {
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
