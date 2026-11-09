package agent

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true, "out": true,
	".venv": true, "venv": true, "__pycache__": true, ".cache": true, ".gradle": true, ".next": true, "Library": true, "Intermediate": true, "Binaries": true}

// ScanRepos finds git working copies under roots and reads their state. It does
// not descend into a repository, and a problem with one repository is recorded on
// that repository rather than failing the scan.
func ScanRepos(ctx context.Context, roots []string, maxDepth int) ([]model.ReportRepo, []string) {
	out := []model.ReportRepo{}
	errs := []string{}
	for _, root := range roots {
		root = filepath.Clean(root)
		base := strings.Count(root, string(os.PathSeparator))
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil || !d.IsDir() {
				return nil
			}
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if strings.Count(p, string(os.PathSeparator))-base > maxDepth {
				return filepath.SkipDir
			}
			if st, err := os.Stat(filepath.Join(p, ".git")); err == nil && st.IsDir() {
				out = append(out, readRepo(ctx, p))
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			errs = append(errs, "git 扫描 "+root+": "+err.Error())
		}
	}
	return out, errs
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// fsmonitor is a command git would run on our behalf; a scanner walking many
	// checkouts has no business running one.
	full := append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)
	out, err := exec.CommandContext(c, "git", full...).Output()
	return strings.TrimRight(string(out), "\r\n"), err
}

func readRepo(ctx context.Context, dir string) model.ReportRepo {
	r := model.ReportRepo{Path: filepath.ToSlash(dir)}
	status, err := git(ctx, dir, "status", "--porcelain=v2", "--branch", "--show-stash")
	if err != nil {
		r.Error = "git status 失败"
		return r
	}
	parseStatus(status, &r)
	if remote, err := git(ctx, dir, "remote", "get-url", "origin"); err == nil {
		r.Remote = SanitizeRemote(remote)
	}
	if email, err := git(ctx, dir, "config", "user.email"); err == nil {
		r.UserEmail = email
	}
	if ts, err := git(ctx, dir, "log", "-1", "--format=%ct"); err == nil {
		if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
			t := time.Unix(n, 0).UTC()
			r.LastCommitAt = &t
		}
	}
	return r
}

// parseStatus reads `git status --porcelain=v2 --branch --show-stash`.
func parseStatus(s string, r *model.ReportRepo) {
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			r.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.upstream "):
			r.HasUpstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(line)
			if len(fields) == 4 {
				r.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
				r.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
			}
		case strings.HasPrefix(line, "# stash "):
			r.Stash, _ = strconv.Atoi(strings.TrimPrefix(line, "# stash "))
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			r.Dirty++
		}
	}
}

// SanitizeRemote removes anything credential-shaped from a remote URL before it
// leaves the machine. A personal access token pasted into a clone URL is common.
func SanitizeRemote(raw string) string {
	host, path, ok := model.ParseRemote(raw)
	if !ok {
		return ""
	}
	if i := strings.Index(raw, "://"); i > 0 {
		return strings.TrimSpace(raw)[:strings.Index(strings.TrimSpace(raw), "://")] + "://" + host + "/" + path
	}
	return host + ":" + path
}
