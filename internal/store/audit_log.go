package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/px0-ai/px0/internal/db"
	"github.com/px0-ai/px0/internal/model"
)

func InsertAuditLog(ctx context.Context, orgID uuid.UUID, actorID *uuid.UUID, action, entityType string, entityID *uuid.UUID, metadata map[string]any) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO audit_logs (org_id, actor_id, action, entity_type, entity_id, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		orgID, actorID, action, entityType, entityID, metadata,
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func ListAuditLogsForOrg(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]*model.AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := db.Pool.Query(ctx,
		`SELECT id, org_id, actor_id, action, entity_type, entity_id, metadata, created_at
		 FROM audit_logs
		 WHERE org_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		orgID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*model.AuditLog
	for rows.Next() {
		log := &model.AuditLog{}
		if err := rows.Scan(
			&log.ID,
			&log.OrgID,
			&log.ActorID,
			&log.Action,
			&log.EntityType,
			&log.EntityID,
			&log.Metadata,
			&log.CreatedAt,
		); err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}
