package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
)

// WikiSyncResult represents the result of Wiki LDAP sync with RustFS storage allocation.
type WikiSyncResult struct {
	Created        int      `json:"created"`
	Updated        int      `json:"updated"`
	Skipped        int      `json:"skipped"`
	BucketsCreated int      `json:"buckets_created"`
	Errors         []string `json:"errors,omitempty"`
	Details        []string `json:"details,omitempty"`
}

// WikiSyncLDAPUsers reads AD users and creates per-user RustFS buckets for Wiki.
func WikiSyncLDAPUsers(d *Deps) (*WikiSyncResult, error) {
	result := &WikiSyncResult{
		Errors:  []string{},
		Details: []string{},
	}

	log.Info().Msg("Starting Wiki LDAP sync with RustFS storage allocation")

	// 1. Query AD for active users
	baseDN := d.Cfg.BaseDN()
	filter := "(&(objectClass=user)(!(objectClass=computer))(!(userAccountControl:1.2.840.113556.1.4.803:=2)))"
	attrs := []string{"sAMAccountName", "mail", "givenName", "sn", "displayName"}

	adUsers, err := d.LDAP.SearchSubtree(baseDN, filter, attrs)
	if err != nil {
		return nil, fmt.Errorf("LDAP search failed: %w", err)
	}

	log.Info().Int("count", len(adUsers)).Msg("Found AD users for Wiki sync")

	// 2. Create RustFS (S3) client
	ep := d.Cfg.RustFSEndpoint
	if strings.HasPrefix(ep, "http://") {
		ep = ep[7:]
	} else if strings.HasPrefix(ep, "https://") {
		ep = ep[8:]
	}

	s3Client, err := minio.New(ep, &minio.Options{
		Creds:  credentials.NewStaticV4(d.Cfg.RustFSAccessKey, d.Cfg.RustFSSecretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("RustFS client creation failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// System accounts to skip
	systemAccounts := map[string]bool{
		"admin": true, "system": true, "system-bot": true,
		"guest": true, "advisor": true,
	}

	// 3. Process each AD user
	for _, adUser := range adUsers {
		username := strings.ToLower(adUser.Get("sAMAccountName"))
		if username == "" {
			continue
		}

		if systemAccounts[username] {
			result.Skipped++
			continue
		}

		bucketName := sanitizeBucketName("wiki", username)

		// Check if bucket already exists
		exists, err := s3Client.BucketExists(ctx, bucketName)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Check bucket %s: %v", bucketName, err))
			continue
		}

		if exists {
			result.Skipped++
			result.Details = append(result.Details, fmt.Sprintf("Bucket exists: %s", bucketName))
			continue
		}

		// Create bucket
		if err := s3Client.MakeBucket(ctx, bucketName, minio.MakeBucketOptions{}); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Create bucket %s: %v", bucketName, err))
			continue
		}

		result.BucketsCreated++
		result.Created++
		displayName := adUser.Get("displayName")
		if displayName == "" {
			displayName = username
		}
		result.Details = append(result.Details, fmt.Sprintf("Created bucket: %s (%s)", bucketName, displayName))
		log.Info().Str("bucket", bucketName).Str("user", username).Msg("Created Wiki RustFS bucket")
	}

	log.Info().
		Int("buckets_created", result.BucketsCreated).
		Int("skipped", result.Skipped).
		Int("errors", len(result.Errors)).
		Msg("Wiki LDAP sync completed")

	return result, nil
}

// sanitizeBucketName converts a prefix+username to a valid S3 bucket name.
// S3 rules: lowercase, 3-63 chars, alphanumeric + hyphens only.
func sanitizeBucketName(prefix, username string) string {
	name := strings.ToLower(prefix + "-" + username)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) < 3 {
		result = result + "---"[:3-len(result)]
	}
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}
