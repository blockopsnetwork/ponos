package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	releaseRepo   = "blockopsnetwork/ponos"
	githubAPIBase = "https://api.github.com/repos/" + releaseRepo
	githubBaseURL = "https://github.com/" + releaseRepo + "/releases/download"
)

type upgradeOptions struct {
	version  string
	noVerify bool
	dryRun   bool
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func runUpgrade(args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	version := fs.String("version", "", "target version (default: latest)")
	noVerify := fs.Bool("no-verify", false, "skip checksum verification")
	dryRun := fs.Bool("dry-run", false, "print actions without changes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := upgradeOptions{
		version:  strings.TrimSpace(*version),
		noVerify: *noVerify,
		dryRun:   *dryRun,
	}

	targetVersion, err := resolveTargetVersion(opts.version)
	if err != nil {
		return err
	}

	assetName, err := resolveAssetName()
	if err != nil {
		return err
	}

	if opts.dryRun {
		fmt.Printf("Would install ponos %s (%s) from %s\n", targetVersion, assetName, githubBaseURL)
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to resolve current binary path: %w", err)
	}

	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlink for %s: %w", execPath, err)
	}

	fmt.Printf("Upgrading ponos to %s...\n", targetVersion)

	tmpDir, err := os.MkdirTemp("", "ponos-upgrade-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	assetURL := fmt.Sprintf("%s/%s/%s", githubBaseURL, targetVersion, assetName)
	checksumURL := fmt.Sprintf("%s/%s/checksums.txt", githubBaseURL, targetVersion)

	assetPath := filepath.Join(tmpDir, assetName)
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")

	if err := downloadFile(assetURL, assetPath); err != nil {
		return err
	}
	if err := downloadFile(checksumURL, checksumsPath); err != nil {
		return err
	}

	if !opts.noVerify {
		if err := verifyChecksum(assetPath, checksumsPath, assetName); err != nil {
			return err
		}
	}

	newBinaryPath, err := extractBinary(assetPath, tmpDir)
	if err != nil {
		return err
	}

	if err := replaceBinary(execPath, newBinaryPath); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s; rerun with sudo", execPath)
		}
		return err
	}

	fmt.Printf("ponos updated to %s\n", targetVersion)
	return nil
}

func resolveTargetVersion(versionFlag string) (string, error) {
	if versionFlag != "" {
		if strings.HasPrefix(versionFlag, "v") {
			return versionFlag, nil
		}
		return "v" + versionFlag, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, githubAPIBase+"/releases/latest", nil)
	if err != nil {
		return "", fmt.Errorf("failed to build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch latest release: %s", resp.Status)
	}
	var info releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("failed to parse release response: %w", err)
	}
	if info.TagName == "" {
		return "", fmt.Errorf("latest release tag not found")
	}
	return info.TagName, nil
}

func resolveAssetName() (string, error) {
	var osName string
	switch runtime.GOOS {
	case "darwin":
		osName = "Darwin"
	case "linux":
		osName = "Linux"
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	var archName string
	switch runtime.GOARCH {
	case "amd64":
		archName = "x86_64"
	case "arm64":
		archName = "arm64"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	return fmt.Sprintf("ponos_%s_%s.tar.gz", osName, archName), nil
}

func downloadFile(url, path string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: %s", url, resp.Status)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", path, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func verifyChecksum(assetPath, checksumPath, assetName string) error {
	content, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("failed to read checksums: %w", err)
	}

	var expected string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == assetName {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum not found for %s", assetName)
	}

	file, err := os.Open(assetPath)
	if err != nil {
		return fmt.Errorf("failed to open asset: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to hash asset: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(expected, actual) {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

func extractBinary(archivePath, dstDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("failed to read gzip: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("failed to read archive: %w", err)
		}
		if filepath.Base(header.Name) != "ponos" {
			continue
		}

		outPath := filepath.Join(dstDir, "ponos")
		outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", fmt.Errorf("failed to create binary: %w", err)
		}
		if _, err := io.Copy(outFile, tarReader); err != nil {
			outFile.Close()
			return "", fmt.Errorf("failed to extract binary: %w", err)
		}
		if err := outFile.Close(); err != nil {
			return "", fmt.Errorf("failed to close binary: %w", err)
		}
		return outPath, nil
	}
	return "", fmt.Errorf("binary not found in archive")
}

func replaceBinary(targetPath, newBinaryPath string) error {
	targetDir := filepath.Dir(targetPath)
	tmpFile, err := os.CreateTemp(targetDir, "ponos-upgrade-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write([]byte{}); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to initialize temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	data, err := os.ReadFile(newBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to read new binary: %w", err)
	}
	if err := os.WriteFile(tmpName, data, 0o755); err != nil {
		return fmt.Errorf("failed to write temp binary: %w", err)
	}

	if err := os.Rename(tmpName, targetPath); err != nil {
		return err
	}
	return os.Chmod(targetPath, 0o755)
}
