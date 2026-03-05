// Package builder provides site build pipeline (clone → install → build → deploy).
package builder

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/docker"
	"github.com/triangles/polyon-core/internal/store"
)

// Builder manages async site builds.
type Builder struct {
	docker *docker.Client
	store  *store.Store
	mu     sync.Mutex
	active map[string]context.CancelFunc // siteID → cancel
	Logs   *LogStream
}

// New creates a Builder.
func New(dc *docker.Client, st *store.Store) *Builder {
	return &Builder{
		docker: dc,
		store:  st,
		active: make(map[string]context.CancelFunc),
		Logs:   NewLogStream(),
	}
}

// frameworkPresets maps framework names to build/output defaults.
var frameworkPresets = map[string]struct{ buildCmd, outputDir string }{
	"gatsby": {"npm run build", "public"},
	"next":   {"npm run build", "out"},
	"astro":  {"npm run build", "dist"},
	"vite":   {"npm run build", "dist"},
	"hugo":   {"hugo --minify", "public"},
	"nuxt":   {"npm run generate", ".output/public"},
	"static": {"", "."},
}

// BuildGitSite runs the full build pipeline for a git-sourced site.
func (b *Builder) BuildGitSite(siteID string, buildID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)

	b.mu.Lock()
	// Cancel previous build if running
	if prev, ok := b.active[siteID]; ok {
		prev()
	}
	b.active[siteID] = cancel
	b.mu.Unlock()

	defer func() {
		cancel()
		b.mu.Lock()
		delete(b.active, siteID)
		b.mu.Unlock()
	}()

	b.run(ctx, siteID, buildID)
}

// BuildKey returns the log stream key for a site.
func BuildKey(siteID string) string { return "site:" + siteID }

func (b *Builder) run(ctx context.Context, siteID string, buildID int) {
	logKey := BuildKey(siteID)
	logBuf := &strings.Builder{}
	appendLog := func(line string) {
		logBuf.WriteString(line + "\n")
		_ = b.store.AppendBuildLog(ctx, buildID, line+"\n")
		b.Logs.Publish(logKey, line)
	}

	defer b.Logs.CloseAll(logKey)

	fail := func(step, msg string) {
		errMsg := fmt.Sprintf("[%s] %s", step, msg)
		appendLog("❌ " + errMsg)
		_ = b.store.UpdateBuild(ctx, buildID, "failed", logBuf.String(), &errMsg)
		_ = b.store.UpdateSiteStatus(ctx, siteID, "error")
	}

	// Load site info
	site, err := b.store.GetSite(ctx, siteID)
	if err != nil {
		fail("init", "site not found: "+err.Error())
		return
	}

	repoURL := ""
	if site.RepoURL != nil {
		repoURL = *site.RepoURL
	}
	branch := "main"
	if site.Branch != nil && *site.Branch != "" {
		branch = *site.Branch
	}

	// Detect framework / fill defaults
	framework := ""
	if site.Framework != nil {
		framework = *site.Framework
	}
	buildCmd := ""
	if site.BuildCmd != nil {
		buildCmd = *site.BuildCmd
	}
	outputDir := "dist"
	if site.OutputDir != nil && *site.OutputDir != "" {
		outputDir = *site.OutputDir
	}

	// Apply preset defaults if framework set but cmd empty
	if framework != "" && buildCmd == "" {
		if p, ok := frameworkPresets[framework]; ok {
			buildCmd = p.buildCmd
			outputDir = p.outputDir
		}
	}

	containerName := fmt.Sprintf("polyon-build-%s", siteID[:8])
	workDir := "/workspace"
	sitesDir := "/data/sites/" + siteID

	appendLog("🚀 빌드 시작: " + site.Name)
	appendLog("📦 레포: " + repoURL + " (" + branch + ")")

	// ── Step 1: Clone ──
	_ = b.store.UpdateBuild(ctx, buildID, "cloning", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "cloning")
	appendLog("\n── Step 1/4: Clone ──")

	// Clean previous source
	b.execInBuildContainer(ctx, containerName, fmt.Sprintf("rm -rf %s/src", workDir), workDir)

	cloneCmd := fmt.Sprintf("git clone --depth 1 --branch %s %s %s/src", branch, repoURL, workDir)
	out, err := b.execInBuildContainer(ctx, containerName, cloneCmd, workDir)
	appendLog(out)
	if err != nil {
		fail("clone", err.Error())
		return
	}
	appendLog("✅ Clone 완료")

	// ── Step 2: Detect framework (if not specified) ──
	if framework == "" {
		appendLog("\n── 프레임워크 자동 감지 ──")
		detected, err := b.detectFramework(ctx, containerName, workDir+"/src")
		if err != nil {
			appendLog("⚠️ 자동 감지 실패, 기본값 사용: " + err.Error())
		} else {
			framework = detected
			if p, ok := frameworkPresets[framework]; ok {
				if buildCmd == "" {
					buildCmd = p.buildCmd
				}
				outputDir = p.outputDir
			}
			appendLog("✅ 감지됨: " + framework)
		}
	}

	// ── Step 3: Install dependencies ──
	_ = b.store.UpdateBuild(ctx, buildID, "installing", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "installing")
	appendLog("\n── Step 2/4: 의존성 설치 ──")

	// Detect package manager: yarn.lock → yarn, pnpm-lock.yaml → pnpm, else npm
	pkgMgr := "npm"
	detectOut, _ := b.execInBuildContainer(ctx, containerName,
		"cd "+workDir+"/src && ls yarn.lock pnpm-lock.yaml package-lock.json 2>/dev/null", workDir+"/src")
	switch {
	case strings.Contains(detectOut, "yarn.lock"):
		pkgMgr = "yarn"
		// Ensure yarn is installed
		b.execInBuildContainer(ctx, containerName, "npm install -g yarn 2>&1", workDir)
	case strings.Contains(detectOut, "pnpm-lock.yaml"):
		pkgMgr = "pnpm"
		b.execInBuildContainer(ctx, containerName, "npm install -g pnpm 2>&1", workDir)
	}
	appendLog("📦 패키지 매니저 감지: " + pkgMgr)

	var installCmd string
	switch pkgMgr {
	case "yarn":
		installCmd = "cd " + workDir + "/src && yarn install 2>&1"
	case "pnpm":
		installCmd = "cd " + workDir + "/src && pnpm install 2>&1"
	default:
		installCmd = "cd " + workDir + "/src && npm install --legacy-peer-deps 2>&1"
	}
	out, err = b.execInBuildContainer(ctx, containerName, installCmd, workDir+"/src")
	appendLog(truncateLog(out, 10000))
	if err != nil {
		fail("install", err.Error())
		return
	}
	appendLog("✅ 의존성 설치 완료")

	// ── Step 4: Build ──
	_ = b.store.UpdateBuild(ctx, buildID, "building", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "building")
	appendLog("\n── Step 3/4: 빌드 ──")

	if buildCmd == "" {
		appendLog("ℹ️ 빌드 명령 없음 (정적 사이트)")
	} else {
		appendLog("$ " + buildCmd)
		fullBuildCmd := "cd " + workDir + "/src && " + buildCmd + " 2>&1"
		out, err = b.execInBuildContainer(ctx, containerName, fullBuildCmd, workDir+"/src")
		appendLog(truncateLog(out, 20000))
		if err != nil {
			// Capture last 2000 chars of output as error detail
			errDetail := err.Error()
			if len(out) > 0 {
				tail := out
				if len(tail) > 2000 {
					tail = tail[len(tail)-2000:]
				}
				errDetail = errDetail + "\n" + tail
			}
			fail("build", errDetail)
			return
		}
		appendLog("✅ 빌드 완료")
	}

	// ── Step 5: Deploy (copy output) ──
	_ = b.store.UpdateBuild(ctx, buildID, "deploying", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "deploying")
	appendLog("\n── Step 4/4: 배포 ──")

	srcPath := workDir + "/src/" + outputDir
	deployCmd := fmt.Sprintf("rm -rf %s/dist && mkdir -p %s/dist && cp -r %s/* %s/dist/ 2>&1",
		sitesDir, sitesDir, srcPath, sitesDir)
	out, err = b.execInBuildContainer(ctx, containerName, deployCmd, workDir)
	appendLog(out)
	if err != nil {
		fail("deploy", "출력 디렉토리 복사 실패: "+err.Error())
		return
	}

	// Cleanup build container
	b.removeBuildContainer(ctx, containerName)

	// Generate nginx vhost
	appendLog("\n── nginx 설정 생성 ──")
	domain := ""
	if site.Domain != nil {
		domain = *site.Domain
	}
	if err := b.DeploySite(ctx, siteID, site.Slug, domain); err != nil {
		appendLog("⚠️ nginx 설정 실패: " + err.Error())
		// Non-fatal: files are deployed, just no vhost yet
	} else {
		appendLog("✅ nginx vhost 생성 완료")
	}

	// Success
	appendLog("\n🎉 배포 완료!")
	appendLog("🔗 " + siteURL(site.Slug, site.Domain))
	_ = b.store.UpdateBuild(ctx, buildID, "success", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "live")

	log.Info().Str("siteId", siteID).Str("slug", site.Slug).Msg("site build success")
}

// detectFramework reads package.json to guess the framework.
func (b *Builder) detectFramework(ctx context.Context, containerName, srcDir string) (string, error) {
	out, err := b.execInBuildContainer(ctx, containerName,
		"cat "+srcDir+"/package.json 2>/dev/null || echo '{}'", srcDir)
	if err != nil {
		return "static", nil
	}

	lower := strings.ToLower(out)
	switch {
	case strings.Contains(lower, "\"gatsby\""):
		return "gatsby", nil
	case strings.Contains(lower, "\"next\""):
		return "next", nil
	case strings.Contains(lower, "\"astro\""):
		return "astro", nil
	case strings.Contains(lower, "\"nuxt\""):
		return "nuxt", nil
	case strings.Contains(lower, "\"vite\""):
		return "vite", nil
	default:
		// Check for Hugo
		_, err := b.execInBuildContainer(ctx, containerName,
			"test -f "+srcDir+"/hugo.toml -o -f "+srcDir+"/config.toml && echo hugo", srcDir)
		if err == nil {
			return "hugo", nil
		}
		return "static", nil
	}
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... (truncated)"
}

// CancelBuild cancels an active build for a site.
func (b *Builder) CancelBuild(siteID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cancel, ok := b.active[siteID]; ok {
		cancel()
	}
}

// IsBuilding returns true if a build is in progress for the site.
func (b *Builder) IsBuilding(siteID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.active[siteID]
	return ok
}
