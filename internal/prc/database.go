package prc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// DatabaseProvider provisions PostgreSQL databases for modules.
type DatabaseProvider struct {
	Pool *pgxpool.Pool // admin (superuser) connection pool
	Host string        // e.g., "polyon-db"
	Port string        // e.g., "5432"
}

func (p *DatabaseProvider) Type() string        { return "database" }
func (p *DatabaseProvider) DependsOn() []string { return nil }

func (p *DatabaseProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	name := claim.ConfigString("name", claim.ModuleID)
	dbName := "polyon_" + name
	user := "mod_" + name
	password := generatePassword(24)

	// 1. CREATE ROLE (idempotent)
	_, err := p.Pool.Exec(ctx, fmt.Sprintf(
		`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s';
			END IF;
		END $$`, user, user, password))
	if err != nil {
		return nil, fmt.Errorf("create role %s: %w", user, err)
	}

	// 2. CREATE DATABASE (PG 17 compatible: no OWNER clause, then GRANT ALL)
	var exists bool
	p.Pool.QueryRow(ctx, "SELECT true FROM pg_database WHERE datname=$1", dbName).Scan(&exists)
	if !exists {
		_, err = p.Pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", dbName))
		if err != nil {
			return nil, fmt.Errorf("create database %s: %w", dbName, err)
		}
	}
	// Grant full access to module user (PG 17 compatible)
	p.Pool.Exec(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s", dbName, user))

	// PG 15+: public schema requires explicit GRANT
	grantPool, gErr := pgxpool.New(ctx, fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		"polyon", "", p.Host, p.Port, dbName))
	if gErr == nil {
		defer grantPool.Close()
		grantPool.Exec(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s", user))
		grantPool.Exec(ctx, fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO %s", user))
		grantPool.Exec(ctx, fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO %s", user))
	}

	// 3. Extensions
	if exts := claim.ConfigString("extensions", ""); exts != "" {
		extConn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			user, password, p.Host, p.Port, dbName)
		extPool, err := pgxpool.New(ctx, extConn)
		if err == nil {
			defer extPool.Close()
			for _, ext := range strings.Split(exts, ",") {
				ext = strings.TrimSpace(ext)
				if ext != "" {
					extPool.Exec(ctx, fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s", ext))
				}
			}
		}
	}

	url := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		user, password, p.Host, p.Port, dbName)

	return Credentials{
		"url":      url,
		"host":     p.Host,
		"port":     p.Port,
		"database": dbName,
		"user":     user,
		"password": password,
	}, nil
}

func (p *DatabaseProvider) Deprovision(ctx context.Context, claim Claim) error {
	name := claim.ConfigString("name", claim.ModuleID)
	dbName := "polyon_" + name
	user := "mod_" + name

	// Terminate connections
	p.Pool.Exec(ctx, fmt.Sprintf(
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='%s'", dbName))

	// DROP DATABASE
	if _, err := p.Pool.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)); err != nil {
		log.Warn().Err(err).Str("db", dbName).Msg("PRC: DROP DATABASE failed")
	}

	// DROP ROLE
	if _, err := p.Pool.Exec(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", user)); err != nil {
		log.Warn().Err(err).Str("user", user).Msg("PRC: DROP ROLE failed")
	}

	return nil
}

func (p *DatabaseProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	name := claim.ConfigString("name", claim.ModuleID)
	dbName := "polyon_" + name
	var exists bool
	err := p.Pool.QueryRow(ctx, "SELECT true FROM pg_database WHERE datname=$1", dbName).Scan(&exists)
	if err != nil || !exists {
		return StatusNotFound, nil
	}
	return StatusProvisioned, nil
}

// generatePassword creates a secure random password (a-zA-Z0-9, no special chars).
func generatePassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	raw := make([]byte, length)
	rand.Read(raw)
	for i := range b {
		b[i] = charset[int(raw[i])%len(charset)]
	}
	return string(b)
}

// generateHex creates a hex-encoded random string.
func generateHex(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}
