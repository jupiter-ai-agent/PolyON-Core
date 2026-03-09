package prc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// CacheProvider provisions Redis DB numbers for modules.
// Redis has 16 databases (0-15). DB 0 is reserved for system.
// Allocation is tracked in the redis_db_allocations table.
type CacheProvider struct {
	Pool     *pgxpool.Pool // PG pool for allocation tracking
	Host     string        // e.g., "polyon-redis"
	Port     string        // e.g., "6379"
}

func (p *CacheProvider) Type() string        { return "cache" }
func (p *CacheProvider) DependsOn() []string { return nil }

func (p *CacheProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	dbStr := claim.ConfigString("db", "auto")

	var dbNum int

	if dbStr == "auto" {
		// Find next available DB number
		err := p.Pool.QueryRow(ctx,
			`UPDATE redis_db_allocations SET module_id=$1, allocated_at=NOW()
			 WHERE db_number = (
				SELECT db_number FROM redis_db_allocations
				WHERE module_id IS NULL
				ORDER BY db_number LIMIT 1
			 ) RETURNING db_number`,
			claim.ModuleID).Scan(&dbNum)
		if err != nil {
			return nil, fmt.Errorf("no available Redis DB number: %w", err)
		}
	} else {
		n, err := strconv.Atoi(dbStr)
		if err != nil || n < 0 || n > 15 {
			return nil, fmt.Errorf("invalid Redis DB number: %s (must be 0-15)", dbStr)
		}
		dbNum = n
		// Allocate specific number
		res, err := p.Pool.Exec(ctx,
			`UPDATE redis_db_allocations SET module_id=$1, allocated_at=NOW()
			 WHERE db_number=$2 AND module_id IS NULL`,
			claim.ModuleID, dbNum)
		if err != nil {
			return nil, fmt.Errorf("allocate Redis DB %d: %w", dbNum, err)
		}
		if res.RowsAffected() == 0 {
			return nil, fmt.Errorf("Redis DB %d is already allocated", dbNum)
		}
	}

	url := fmt.Sprintf("redis://%s:%s/%d", p.Host, p.Port, dbNum)

	log.Info().Int("db", dbNum).Str("module", claim.ModuleID).Msg("PRC: Redis DB allocated")

	return Credentials{
		"url":  url,
		"host": p.Host,
		"port": p.Port,
		"db":   strconv.Itoa(dbNum),
	}, nil
}

func (p *CacheProvider) Deprovision(ctx context.Context, claim Claim) error {
	_, err := p.Pool.Exec(ctx,
		`UPDATE redis_db_allocations SET module_id=NULL, allocated_at=NULL WHERE module_id=$1`,
		claim.ModuleID)
	if err != nil {
		log.Warn().Err(err).Str("module", claim.ModuleID).Msg("PRC: Redis DB deallocation failed")
	}
	return nil
}

func (p *CacheProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	var dbNum int
	err := p.Pool.QueryRow(ctx,
		`SELECT db_number FROM redis_db_allocations WHERE module_id=$1`,
		claim.ModuleID).Scan(&dbNum)
	if err != nil {
		return StatusNotFound, nil
	}
	return StatusProvisioned, nil
}
