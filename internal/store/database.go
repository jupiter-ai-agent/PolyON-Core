package store

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// replaceDBInConnStr replaces the database name in a PostgreSQL connection string.
func replaceDBInConnStr(connStr string, newDB string) string {
	u, err := url.Parse(connStr)
	if err != nil || u.Scheme == "" {
		// Try keyword=value format
		if strings.Contains(connStr, "dbname=") {
			parts := strings.Fields(connStr)
			for i, p := range parts {
				if strings.HasPrefix(p, "dbname=") {
					parts[i] = "dbname=" + newDB
				}
			}
			return strings.Join(parts, " ")
		}
		return ""
	}
	u.Path = "/" + newDB
	return u.String()
}

// CreateModuleDatabase creates a new database and user for a module.
func (s *Store) CreateModuleDatabase(ctx context.Context, dbName, dbUser, dbPassword string) error {
	log.Info().Str("db_name", dbName).Str("db_user", dbUser).Msg("Creating module database")

	// Create user (idempotent — skip if exists, update password)
	createUserSQL := fmt.Sprintf(`CREATE USER "%s" WITH PASSWORD '%s'`, dbUser, dbPassword)
	if _, err := s.pool.Exec(ctx, createUserSQL); err != nil {
		// Check if role already exists
		if strings.Contains(err.Error(), "already exists") {
			log.Info().Str("db_user", dbUser).Msg("User already exists, updating password")
			alterPwSQL := fmt.Sprintf(`ALTER USER "%s" WITH PASSWORD '%s'`, dbUser, dbPassword)
			s.pool.Exec(ctx, alterPwSQL)
		} else {
			return fmt.Errorf("failed to create user %s: %w", dbUser, err)
		}
	}

	// Create database (idempotent — skip if exists)
	createDbSQL := fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)
	if _, err := s.pool.Exec(ctx, createDbSQL); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			log.Info().Str("db_name", dbName).Msg("Database already exists, reusing")
		} else {
			return fmt.Errorf("failed to create database %s: %w", dbName, err)
		}
	}

	// Transfer ownership + grant privileges
	alterSQL := fmt.Sprintf(`ALTER DATABASE "%s" OWNER TO "%s"`, dbName, dbUser)
	if _, err := s.pool.Exec(ctx, alterSQL); err != nil {
		log.Warn().Err(err).Msg("Failed to set database owner, granting privileges instead")
	}
	grantSQL := fmt.Sprintf(`GRANT ALL PRIVILEGES ON DATABASE "%s" TO "%s"`, dbName, dbUser)
	if _, err := s.pool.Exec(ctx, grantSQL); err != nil {
		log.Warn().Err(err).Str("db_name", dbName).Str("db_user", dbUser).Msg("Failed to grant privileges")
	}

	// Grant schema permissions (connect to the new database)
	connStr := s.pool.Config().ConnString()
	// Replace database name in connection string
	newConnStr := replaceDBInConnStr(connStr, dbName)
	if newConnStr != "" {
		newPool, err := pgxpool.New(ctx, newConnStr)
		if err == nil {
			defer newPool.Close()
			schemaSQL := fmt.Sprintf(`GRANT ALL ON SCHEMA public TO "%s"`, dbUser)
			if _, err := newPool.Exec(ctx, schemaSQL); err != nil {
				log.Warn().Err(err).Msg("Failed to grant schema permissions")
			}
		}
	}

	log.Info().Str("db_name", dbName).Str("db_user", dbUser).Msg("Module database created successfully")
	return nil
}

// DeleteModuleDatabase drops a database and user for a module.
func (s *Store) DeleteModuleDatabase(ctx context.Context, dbName, dbUser string) error {
	log.Info().Str("db_name", dbName).Str("db_user", dbUser).Msg("Deleting module database")

	// Terminate active connections to the database
	terminateSQL := `
		SELECT pg_terminate_backend(pid) 
		FROM pg_stat_activity 
		WHERE datname = $1 AND pid <> pg_backend_pid()`
	if _, err := s.pool.Exec(ctx, terminateSQL, dbName); err != nil {
		log.Warn().Err(err).Str("db_name", dbName).Msg("Failed to terminate connections")
	}

	// Drop database
	dropDbSQL := fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)
	if _, err := s.pool.Exec(ctx, dropDbSQL); err != nil {
		log.Warn().Err(err).Str("db_name", dbName).Msg("Failed to drop database")
	}

	// Drop user
	dropUserSQL := fmt.Sprintf(`DROP USER IF EXISTS "%s"`, dbUser)
	if _, err := s.pool.Exec(ctx, dropUserSQL); err != nil {
		log.Warn().Err(err).Str("db_user", dbUser).Msg("Failed to drop user")
	}

	log.Info().Str("db_name", dbName).Str("db_user", dbUser).Msg("Module database deleted")
	return nil
}

// CheckDatabaseExists checks if a database exists.
func (s *Store) CheckDatabaseExists(ctx context.Context, dbName string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`
	err := s.pool.QueryRow(ctx, query, dbName).Scan(&exists)
	return exists, err
}

// CheckUserExists checks if a user exists.
func (s *Store) CheckUserExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM pg_user WHERE usename = $1)`
	err := s.pool.QueryRow(ctx, query, username).Scan(&exists)
	return exists, err
}

// GeneratePassword generates a secure random password for database users.
func GeneratePassword(length int) (string, error) {
	if length <= 0 {
		length = 24
	}

	// Character set: a-zA-Z0-9-_
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	charsetLen := big.NewInt(int64(len(charset)))

	password := make([]byte, length)
	for i := range password {
		randomIndex, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random number: %w", err)
		}
		password[i] = charset[randomIndex.Int64()]
	}

	return string(password), nil
}

// GetDatabaseConnection builds a connection string for a module database.
func GetDatabaseConnection(dbHost, dbPort, dbName, dbUser, dbPassword string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPassword, dbHost, dbPort, dbName)
}