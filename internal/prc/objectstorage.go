package prc

import (
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
)

// ObjectStorageProvider provisions RustFS/S3 buckets for modules.
type ObjectStorageProvider struct {
	Endpoint  string // e.g., "polyon-rustfs:9000"
	AccessKey string // admin access key
	SecretKey string // admin secret key
}

func (p *ObjectStorageProvider) Type() string        { return "objectStorage" }
func (p *ObjectStorageProvider) DependsOn() []string { return nil }

func (p *ObjectStorageProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	bucket := claim.ConfigString("bucket", claim.ModuleID)

	client, err := p.newClient()
	if err != nil {
		return nil, fmt.Errorf("S3 client init: %w", err)
	}

	// Create bucket (idempotent)
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket check: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("bucket create: %w", err)
		}
		log.Info().Str("bucket", bucket).Msg("PRC: S3 bucket created")
	}

	// For now, share admin credentials per module.
	// Phase 2: RustFS IAM API for per-module access keys.
	moduleAccess := fmt.Sprintf("mod-%s-access", claim.ModuleID)
	moduleSecret := generatePassword(32)

	// RustFS doesn't have a standard IAM API yet, so we use admin keys
	// but track the intended per-module keys for future migration.
	return Credentials{
		"endpoint":  "http://" + p.Endpoint,
		"bucket":    bucket,
		"accessKey": p.AccessKey,  // admin key (Phase 1)
		"secretKey": p.SecretKey,  // admin key (Phase 1)
		"_intendedAccessKey": moduleAccess, // for Phase 2 IAM
		"_intendedSecretKey": moduleSecret,
	}, nil
}

func (p *ObjectStorageProvider) Deprovision(ctx context.Context, claim Claim) error {
	bucket := claim.ConfigString("bucket", claim.ModuleID)

	client, err := p.newClient()
	if err != nil {
		log.Warn().Err(err).Msg("PRC: S3 client init failed during deprovision")
		return nil
	}

	exists, _ := client.BucketExists(ctx, bucket)
	if !exists {
		return nil
	}

	// Remove all objects first (required by S3 API)
	objectsCh := client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true})
	for obj := range objectsCh {
		if obj.Err != nil {
			continue
		}
		client.RemoveObject(ctx, bucket, obj.Key, minio.RemoveObjectOptions{})
	}

	if err := client.RemoveBucket(ctx, bucket); err != nil {
		log.Warn().Err(err).Str("bucket", bucket).Msg("PRC: bucket removal failed")
	}

	return nil
}

func (p *ObjectStorageProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	bucket := claim.ConfigString("bucket", claim.ModuleID)
	client, err := p.newClient()
	if err != nil {
		return StatusError, err
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return StatusError, err
	}
	if !exists {
		return StatusNotFound, nil
	}
	return StatusProvisioned, nil
}

func (p *ObjectStorageProvider) newClient() (*minio.Client, error) {
	return minio.New(p.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(p.AccessKey, p.SecretKey, ""),
		Secure: false,
	})
}
