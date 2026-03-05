package store

import (
	"context"
	"encoding/json"
	"time"
)

// Site represents a homepage site.
type Site struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Method     string          `json:"method"` // "editor" | "git" | "strapi"
	Status     string          `json:"status"`
	Template   string          `json:"template,omitempty"` // "corporate" | "landing" | "blog" | "blank"
	LayoutJSON json.RawMessage `json:"layoutJson,omitempty"`
	RepoURL    *string         `json:"repoUrl,omitempty"`
	Branch     *string         `json:"branch,omitempty"`
	Framework  *string         `json:"framework,omitempty"`
	BuildCmd   *string         `json:"buildCmd,omitempty"`
	OutputDir  *string         `json:"outputDir,omitempty"`
	Domain     *string         `json:"domain,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	UpdatedAt  time.Time       `json:"updatedAt"`
}

// SiteBuild represents a build record.
type SiteBuild struct {
	ID         int        `json:"id"`
	SiteID     string     `json:"siteId"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Log        string     `json:"log"`
	Trigger    string     `json:"trigger"`
	Error      *string    `json:"error,omitempty"`
}

// CreateSite inserts a new site.
func (s *Store) CreateSite(ctx context.Context, site *Site) error {
	tmpl := site.Template
	if tmpl == "" {
		tmpl = "corporate"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sites (id, name, slug, method, status, template, layout_json, repo_url, branch, framework, build_cmd, output_dir, domain)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		site.ID, site.Name, site.Slug, site.Method, site.Status, tmpl,
		site.LayoutJSON, site.RepoURL, site.Branch, site.Framework,
		site.BuildCmd, site.OutputDir, site.Domain,
	)
	return err
}

// ListSites returns all sites ordered by created_at desc.
func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, slug, method, status, COALESCE(template,'corporate'), layout_json, repo_url, branch, framework, build_cmd, output_dir, domain, created_at, updated_at
		 FROM sites ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var st Site
		if err := rows.Scan(&st.ID, &st.Name, &st.Slug, &st.Method, &st.Status, &st.Template,
			&st.LayoutJSON, &st.RepoURL, &st.Branch, &st.Framework,
			&st.BuildCmd, &st.OutputDir, &st.Domain,
			&st.CreatedAt, &st.UpdatedAt); err != nil {
			return nil, err
		}
		sites = append(sites, st)
	}
	if sites == nil {
		sites = []Site{}
	}
	return sites, nil
}

// GetSite returns a single site by ID.
func (s *Store) GetSite(ctx context.Context, id string) (*Site, error) {
	var st Site
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, method, status, COALESCE(template,'corporate'), layout_json, repo_url, branch, framework, build_cmd, output_dir, domain, created_at, updated_at
		 FROM sites WHERE id=$1`, id).
		Scan(&st.ID, &st.Name, &st.Slug, &st.Method, &st.Status, &st.Template,
			&st.LayoutJSON, &st.RepoURL, &st.Branch, &st.Framework,
			&st.BuildCmd, &st.OutputDir, &st.Domain,
			&st.CreatedAt, &st.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// UpdateSiteStatus updates a site's status.
func (s *Store) UpdateSiteStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sites SET status=$1, updated_at=NOW() WHERE id=$2`, status, id)
	return err
}

// UpdateSiteLayout saves the editor layout JSON.
func (s *Store) UpdateSiteLayout(ctx context.Context, id string, layout json.RawMessage) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sites SET layout_json=$1, updated_at=NOW() WHERE id=$2`, layout, id)
	return err
}

// DeleteSite removes a site.
func (s *Store) DeleteSite(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sites WHERE id=$1`, id)
	return err
}

// CreateBuild inserts a new build record and returns its ID.
func (s *Store) CreateBuild(ctx context.Context, siteID, trigger string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO site_builds (site_id, trigger) VALUES ($1, $2) RETURNING id`,
		siteID, trigger).Scan(&id)
	return id, err
}

// UpdateBuild updates a build record.
func (s *Store) UpdateBuild(ctx context.Context, id int, status string, log string, buildErr *string) error {
	var finished *time.Time
	if status == "success" || status == "failed" {
		now := time.Now()
		finished = &now
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE site_builds SET status=$1, log=$2, error=$3, finished_at=$4 WHERE id=$5`,
		status, log, buildErr, finished, id)
	return err
}

// AppendBuildLog appends text to a build's log.
func (s *Store) AppendBuildLog(ctx context.Context, id int, text string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE site_builds SET log = log || $1 WHERE id = $2`, text, id)
	return err
}

// GetBuild returns a single build by ID.
func (s *Store) GetBuild(ctx context.Context, id int) (*SiteBuild, error) {
	var b SiteBuild
	err := s.pool.QueryRow(ctx,
		`SELECT id, site_id, status, started_at, finished_at, log, trigger, error
		 FROM site_builds WHERE id=$1`, id).
		Scan(&b.ID, &b.SiteID, &b.Status, &b.StartedAt, &b.FinishedAt, &b.Log, &b.Trigger, &b.Error)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBuilds returns builds for a site.
func (s *Store) ListBuilds(ctx context.Context, siteID string, limit int) ([]SiteBuild, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, site_id, status, started_at, finished_at, log, trigger, error
		 FROM site_builds WHERE site_id=$1 ORDER BY started_at DESC LIMIT $2`,
		siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var builds []SiteBuild
	for rows.Next() {
		var b SiteBuild
		if err := rows.Scan(&b.ID, &b.SiteID, &b.Status, &b.StartedAt, &b.FinishedAt, &b.Log, &b.Trigger, &b.Error); err != nil {
			return nil, err
		}
		builds = append(builds, b)
	}
	if builds == nil {
		builds = []SiteBuild{}
	}
	return builds, nil
}

// UpdateSiteRepoInfo updates the repo URL and branch for a site.
func (s *Store) UpdateSiteRepoInfo(ctx context.Context, siteID, repoURL, branch string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sites SET repo_url=$1, branch=$2, updated_at=NOW() WHERE id=$3`,
		repoURL, branch, siteID)
	return err
}

// UpdateSiteDomain sets or clears the custom domain for a site.
// Pass nil to clear the domain.
func (s *Store) UpdateSiteDomain(ctx context.Context, siteID string, domain *string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sites SET domain=$1, updated_at=NOW() WHERE id=$2`, domain, siteID)
	return err
}

// UpdateSiteTemplate stores the chosen template name.
func (s *Store) UpdateSiteTemplate(ctx context.Context, siteID, tmpl string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sites SET template=$1, updated_at=NOW() WHERE id=$2`, tmpl, siteID)
	return err
}
