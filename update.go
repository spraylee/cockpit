package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"

	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releasesRepo = "spraylee/cockpit"

// Download cap: a release archive is a few MB, so anything near this is not
// the artifact we asked for.
const maxDownload = 200 << 20

func releaseTriple() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "aarch64-apple-darwin", nil
	case "darwin/amd64":
		return "x86_64-apple-darwin", nil
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu", nil
	case "linux/amd64":
		return "x86_64-unknown-linux-gnu", nil
	}
	return "", fmt.Errorf("%s/%s 没有预编译版本", runtime.GOOS, runtime.GOARCH)
}

// updateSelf replaces the running binary with the latest release. The running
// process keeps the old inode, and any `ck` symlink keeps pointing at the same
// path, so both names pick up the new build without extra bookkeeping.
func updateSelf() error {
	triple, err := releaseTriple()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	client := &http.Client{Timeout: 60 * time.Second}
	latest, err := latestTag(client)
	if err != nil {
		return err
	}
	if !isNewer(latest, version) {
		fmt.Printf("已是最新版本 %s\n", version)
		return nil
	}

	base := "https://github.com/" + releasesRepo + "/releases/download/" + latest + "/"
	sums, err := fetchBytes(client, base+"SHA256SUMS")
	if err != nil {
		return fmt.Errorf("下载校验文件失败：%w", err)
	}
	asset := fmt.Sprintf("cockpit-%s-%s.tar.gz", latest, triple)
	want, err := checksumFor(string(sums), asset)
	if err != nil {
		return err
	}
	archive, err := fetchBytes(client, base+asset)
	if err != nil {
		return fmt.Errorf("下载 %s 失败：%w", asset, err)
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s 校验失败：期望 %s，实际 %s", asset, want, got)
	}
	binary, err := extractBinary(archive, "cockpit")
	if err != nil {
		return err
	}

	dir := filepath.Dir(self)
	staged, err := os.CreateTemp(dir, ".cockpit-update-")
	if err != nil {
		return fmt.Errorf("无法写入 %s（权限不足？）：%w", dir, err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if _, err := staged.Write(binary); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Chmod(0o755); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, self); err != nil {
		return fmt.Errorf("替换 %s 失败：%w", self, err)
	}
	fmt.Printf("已更新：%s → %s\n", version, latest)
	return nil
}

// latestTag reads the tag from the /releases/latest redirect instead of the
// API: unauthenticated API calls share a 60/hour limit per IP and start
// returning 403 on busy networks, which made updates fail for no good reason.
func latestTag(client *http.Client) (string, error) {
	noRedirect := &http.Client{
		Timeout: client.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodHead, "https://github.com/"+releasesRepo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cockpit/"+version)
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", fmt.Errorf("查询最新版本失败：%w", err)
	}
	resp.Body.Close()
	location := resp.Header.Get("Location")
	const marker = "/releases/tag/"
	i := strings.LastIndex(location, marker)
	if i < 0 {
		return "", fmt.Errorf("查询最新版本失败：%s 未指向发布页（%s）", resp.Status, location)
	}
	tag := location[i+len(marker):]
	if tag == "" {
		return "", errors.New("最新版本信息为空")
	}
	return tag, nil
}

func fetchBytes(client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cockpit/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s：%s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// SHA256SUMS lines are "<hash>  <file>" or "<hash> *<file>".
func checksumFor(sums, asset string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS 中没有 %s", asset)
}

func extractBinary(archive []byte, name string) ([]byte, error) {
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("解压失败：%w", err)
	}
	defer unzipped.Close()
	entries := tar.NewReader(unzipped)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("压缩包里没有 %s", name)
		}
		if err != nil {
			return nil, fmt.Errorf("读取压缩包失败：%w", err)
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != name {
			continue
		}
		return io.ReadAll(io.LimitReader(entries, maxDownload))
	}
}

func isNewer(candidate, current string) bool {
	if current == "dev" {
		return candidate != ""
	}
	return compareVersions(candidate, current) > 0
}

func compareVersions(a, b string) int {
	left, right := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if left[i] != right[i] {
			if left[i] > right[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	for i, part := range strings.SplitN(v, ".", 3) {
		digits := strings.TrimFunc(part, func(r rune) bool { return r < '0' || r > '9' })
		out[i], _ = strconv.Atoi(digits)
	}
	return out
}
