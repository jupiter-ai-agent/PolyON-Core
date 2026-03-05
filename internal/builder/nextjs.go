package builder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
)

// BuildNextJSSitePreview builds a draft preview of a Next.js site.
// Output is served at /preview/{siteID}/ instead of /data/sites/{siteID}/dist.
func (b *Builder) BuildNextJSSitePreview(siteID string, buildID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	b.runNextJSPreview(ctx, siteID, buildID)
}

// BuildGitSitePreview builds a draft preview of a git-sourced site.
func (b *Builder) BuildGitSitePreview(siteID string, buildID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	b.runNextJSPreview(ctx, siteID, buildID) // reuse nextjs preview runner (works for git too)
}

func (b *Builder) runNextJSPreview(ctx context.Context, siteID string, buildID int) {
	logKey := BuildKey(siteID)
	logBuf := &strings.Builder{}

	appendLog := func(line string) {
		logBuf.WriteString(line + "\n")
		if b.store != nil {
			_ = b.store.AppendBuildLog(ctx, buildID, line+"\n")
		}
		b.Logs.Publish(logKey, line)
	}
	defer b.Logs.CloseAll(logKey)

	fail := func(step, msg string) {
		errMsg := fmt.Sprintf("[%s] %s", step, msg)
		appendLog("❌ " + errMsg)
		if b.store != nil {
			_ = b.store.UpdateBuild(ctx, buildID, "failed", logBuf.String(), &errMsg)
		}
	}

	site, err := b.store.GetSite(ctx, siteID)
	if err != nil {
		fail("init", "site not found: "+err.Error())
		return
	}

	appendLog("👁 프리뷰 빌드 시작: " + site.Name)

	cfg := config.Get()
	giteaURL := cfg.GiteaURL
	if giteaURL == "" {
		giteaURL = os.Getenv("GITEA_URL")
	}
	giteaToken := os.Getenv("GITEA_TOKEN")
	branch := "main"
	if site.Branch != nil && *site.Branch != "" {
		branch = *site.Branch
	}

	cloneURL := buildGiteaCloneURL(giteaURL, giteaToken, "polyon", site.Slug)
	tmpDir, err := os.MkdirTemp("", "polyon-preview-"+siteID[:8]+"-")
	if err != nil {
		fail("init", "임시 디렉토리 생성 실패: "+err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	// git clone
	if _, cloneErr := b.runHostCmd(ctx, appendLog, giteaToken, "git",
		"clone", "--depth", "1", "--branch", branch, cloneURL, tmpDir+"/src"); cloneErr != nil {
		fail("clone", "git clone 실패: "+maskToken(cloneErr.Error(), giteaToken))
		return
	}

	strapiURL := cfg.StrapiURL
	if strapiURL == "" {
		strapiURL = os.Getenv("STRAPI_URL")
	}

	outDir := tmpDir + "/out"
	_ = os.MkdirAll(outDir, 0755)

	// Build with STRAPI_DRAFT_LOCALE flag to fetch draft content
	dockerArgs := []string{
		"run", "--rm",
		"--name", "polyon-preview-" + siteID[:8],
		"-v", tmpDir + "/src:/app",
		"-v", outDir + ":/out",
		"-e", "NEXT_PUBLIC_STRAPI_URL=" + strapiURL,
		"-e", "STRAPI_URL=" + strapiURL,
		"-e", "NEXT_PUBLIC_PREVIEW=true",
		"-e", "NODE_ENV=production",
		"-e", "CI=true",
		"--network", "polyon-net",
		"node:20-alpine",
		"sh", "-c",
		"cd /app && npm install --legacy-peer-deps 2>&1 && npm run build 2>&1 && " +
			"if [ -d /app/out ]; then cp -r /app/out/. /out/; fi",
	}

	if _, buildErr := b.runHostCmd(ctx, appendLog, "", "docker", dockerArgs...); buildErr != nil {
		fail("build", "프리뷰 빌드 실패: "+buildErr.Error())
		return
	}

	// Deploy to /preview/{siteID}/ in polyon-console container
	deployArgs := []string{
		"run", "--rm",
		"-v", outDir + ":/src-out:ro",
		"-v", "polyon-sites-data:/data",
		"alpine:3.19",
		"sh", "-c",
		fmt.Sprintf(
			"rm -rf /data/preview/%s && mkdir -p /data/preview/%s && cp -r /src-out/. /data/preview/%s/",
			siteID, siteID, siteID,
		),
	}

	if _, deployErr := b.runHostCmd(ctx, appendLog, "", "docker", deployArgs...); deployErr != nil {
		fail("deploy", "프리뷰 배포 실패: "+deployErr.Error())
		return
	}

	appendLog("✅ 프리뷰 준비 완료: /preview/" + siteID + "/")
	_ = b.store.UpdateBuild(ctx, buildID, "success", logBuf.String(), nil)
}

// BuildNextJSSite builds a Next.js SSG site via docker run --rm.
// Used for 'strapi' method sites — clones from internal Gitea, builds, deploys.
func (b *Builder) BuildNextJSSite(siteID string, buildID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	b.mu.Lock()
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

	b.runNextJS(ctx, siteID, buildID)
}

func (b *Builder) runNextJS(ctx context.Context, siteID string, buildID int) {
	logKey := BuildKey(siteID)
	logBuf := &strings.Builder{}

	appendLog := func(line string) {
		logBuf.WriteString(line + "\n")
		if b.store != nil {
			_ = b.store.AppendBuildLog(ctx, buildID, line+"\n")
		}
		b.Logs.Publish(logKey, line)
	}

	defer b.Logs.CloseAll(logKey)

	fail := func(step, msg string) {
		errMsg := fmt.Sprintf("[%s] %s", step, msg)
		appendLog("❌ " + errMsg)
		if b.store != nil {
			_ = b.store.UpdateBuild(ctx, buildID, "failed", logBuf.String(), &errMsg)
			_ = b.store.UpdateSiteStatus(ctx, siteID, "error")
		}
	}

	// Load site info
	site, err := b.store.GetSite(ctx, siteID)
	if err != nil {
		fail("init", "site not found: "+err.Error())
		return
	}

	appendLog("🚀 Next.js SSG 빌드 시작: " + site.Name)
	_ = b.store.UpdateBuild(ctx, buildID, "cloning", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "cloning")

	// ── Resolve clone URL (internal Gitea) ──
	nxCfg := config.Get()
	giteaURL := nxCfg.GiteaURL
	if giteaURL == "" {
		giteaURL = os.Getenv("GITEA_URL")
	}
	giteaToken := os.Getenv("GITEA_TOKEN")

	branch := "main"
	if site.Branch != nil && *site.Branch != "" {
		branch = *site.Branch
	}

	cloneURL := buildGiteaCloneURL(giteaURL, giteaToken, "polyon", site.Slug)
	appendLog(fmt.Sprintf("📦 레포: polyon/%s (%s)", site.Slug, branch))

	// ── Create temp dir ──
	tmpDir, err := os.MkdirTemp("", "polyon-next-"+siteID[:8]+"-")
	if err != nil {
		fail("init", "임시 디렉토리 생성 실패: "+err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)

	// ── Step 1: git clone ──
	appendLog("\n── Step 1/4: Clone ──")
	if _, cloneErr := b.runHostCmd(ctx, appendLog, giteaToken, "git",
		"clone", "--depth", "1", "--branch", branch, cloneURL, tmpDir+"/src"); cloneErr != nil {
		fail("clone", "git clone 실패: "+maskToken(cloneErr.Error(), giteaToken))
		return
	}
	appendLog("✅ Clone 완료")

	// ── Step 2: npm install + next build via docker run --rm ──
	_ = b.store.UpdateBuild(ctx, buildID, "building", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "building")
	appendLog("\n── Step 2/4: Next.js 빌드 (docker) ──")

	strapiURL := nxCfg.StrapiURL
	if strapiURL == "" {
		strapiURL = os.Getenv("STRAPI_URL")
	}

	outDir := tmpDir + "/out"
	if mkErr := os.MkdirAll(outDir, 0755); mkErr != nil {
		fail("build", "출력 디렉토리 생성 실패: "+mkErr.Error())
		return
	}

	// docker run --rm
	// Mount source (ro) and output (rw)
	dockerArgs := []string{
		"run", "--rm",
		"--name", "polyon-nextbuild-" + siteID[:8],
		"-v", tmpDir + "/src:/app",
		"-v", outDir + ":/out",
		"-e", "NEXT_PUBLIC_STRAPI_URL=" + strapiURL,
		"-e", "STRAPI_URL=" + strapiURL,
		"-e", "NODE_ENV=production",
		"-e", "CI=true",
		"-e", "NODE_OPTIONS=--max-old-space-size=4096",
		"--network", "polyon-net",
		"node:20-alpine",
		"sh", "-c",
		"cd /app && npm install --legacy-peer-deps 2>&1 && npm run build 2>&1 && " +
			"if [ -d /app/out ]; then cp -r /app/out/. /out/; fi",
	}

	if _, buildErr := b.runHostCmd(ctx, appendLog, "", "docker", dockerArgs...); buildErr != nil {
		fail("build", "Next.js 빌드 실패: "+buildErr.Error())
		return
	}
	appendLog("✅ Next.js 빌드 완료")

	// ── Step 3: Copy output → polyon-sites-data volume via docker ──
	_ = b.store.UpdateBuild(ctx, buildID, "deploying", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "deploying")
	appendLog("\n── Step 3/4: 배포 (볼륨 복사) ──")

	deployArgs := []string{
		"run", "--rm",
		"-v", outDir + ":/src-out:ro",
		"-v", "polyon-sites-data:/data/sites",
		"alpine:3.19",
		"sh", "-c",
		fmt.Sprintf(
			"rm -rf /data/sites/%s/dist && mkdir -p /data/sites/%s/dist && cp -r /src-out/. /data/sites/%s/dist/",
			siteID, siteID, siteID,
		),
	}

	if _, deployErr := b.runHostCmd(ctx, appendLog, "", "docker", deployArgs...); deployErr != nil {
		fail("deploy", "파일 복사 실패: "+deployErr.Error())
		return
	}
	appendLog("✅ 배포 완료")

	// ── Step 4: nginx vhost ──
	appendLog("\n── Step 4/4: nginx 설정 ──")
	domain := ""
	if site.Domain != nil {
		domain = *site.Domain
	}
	if nginxErr := b.DeploySite(ctx, siteID, site.Slug, domain); nginxErr != nil {
		appendLog("⚠️ nginx 설정 실패 (비치명적): " + nginxErr.Error())
	} else {
		appendLog("✅ nginx vhost 적용")
	}

	// ── Done ──
	appendLog("\n🎉 배포 완료!")
	appendLog("🔗 " + siteURL(site.Slug, site.Domain))
	_ = b.store.UpdateBuild(ctx, buildID, "success", logBuf.String(), nil)
	_ = b.store.UpdateSiteStatus(ctx, siteID, "live")

	log.Info().Str("siteId", siteID).Str("slug", site.Slug).Msg("next.js build success")
}

// runHostCmd executes a host command and streams output via appendLog.
// tokenToMask: if set, replaces token in log output.
func (b *Builder) runHostCmd(ctx context.Context, appendLog func(string), tokenToMask string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf strings.Builder

	pr, pw, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("pipe: %w", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if startErr := cmd.Start(); startErr != nil {
		pw.Close()
		pr.Close()
		return "", fmt.Errorf("start %s: %w", name, startErr)
	}
	pw.Close()

	// Read and stream
	buf := make([]byte, 8192)
	lineAccum := ""
	for {
		n, readErr := pr.Read(buf)
		if n > 0 {
			chunk := maskToken(string(buf[:n]), tokenToMask)
			outBuf.WriteString(chunk)
			combined := lineAccum + chunk
			lines := strings.Split(combined, "\n")
			for i, line := range lines {
				if i < len(lines)-1 {
					if line != "" {
						appendLog(line)
					}
				} else {
					lineAccum = line
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	if lineAccum != "" {
		appendLog(lineAccum)
	}
	pr.Close()

	if waitErr := cmd.Wait(); waitErr != nil {
		return outBuf.String(), waitErr
	}
	return outBuf.String(), nil
}

// buildGiteaCloneURL builds an authenticated git clone URL.
// e.g. http://polyon:TOKEN@polyon-gitea:3000/polyon/slug.git
func buildGiteaCloneURL(baseURL, token, owner, repoName string) string {
	if token == "" {
		return fmt.Sprintf("%s/%s/%s.git", baseURL, owner, repoName)
	}
	parts := strings.SplitN(baseURL, "://", 2)
	if len(parts) != 2 {
		return fmt.Sprintf("%s/%s/%s.git", baseURL, owner, repoName)
	}
	return fmt.Sprintf("%s://polyon:%s@%s/%s/%s.git", parts[0], token, parts[1], owner, repoName)
}

// maskToken replaces a token string with *** in output.
func maskToken(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}
