// Package repository provides PostgreSQL data access for the semantic graph.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
)

// NodeRepository provides data access for sem_nodes.
type NodeRepository struct {
	pool *pgxpool.Pool
}

// NewNodeRepository creates a new NodeRepository.
func NewNodeRepository(pool *pgxpool.Pool) *NodeRepository {
	return &NodeRepository{pool: pool}
}

// Create inserts a new node and returns the created node.
func (r *NodeRepository) Create(ctx context.Context, node *domain.SemNode) error {
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		if node.WorkspaceID != workspaceID {
			return fmt.Errorf("node workspace does not match request tenant")
		}
		status := node.Status
		if status == "" {
			status = "draft"
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO sem_nodes (workspace_id, layer, kind, label, props, version, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			workspaceID, node.Layer, node.Kind, node.Label, node.Props, node.Version, status).Scan(&id)
		if err != nil {
			return fmt.Errorf("create node: %w", err)
		}
		node.ID = id
		return nil
	})
}

// GetByID retrieves a node by its UUID.
func (r *NodeRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.SemNode, error) {
	var node domain.SemNode
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		err := tx.QueryRow(ctx, `
			SELECT id, workspace_id, layer, kind, label, props, version, status, created_at, updated_at
			FROM sem_nodes WHERE id = $1`, id).Scan(
			&node.ID, &node.WorkspaceID, &node.Layer, &node.Kind, &node.Label,
			&node.Props, &node.Version, &node.Status, &node.CreatedAt, &node.UpdatedAt,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("get node: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// List returns nodes, optionally filtered by layer.
//
// When root is empty it lists all nodes in the tenant. Otherwise it returns the
// subgraph reachable from root within depth hops, traversing edges in both
// directions and skipping expired edges (valid_to IS NOT NULL).
func (r *NodeRepository) List(ctx context.Context, layer, root string, depth int) ([]*domain.SemNode, error) {
	var nodes []*domain.SemNode
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		var (
			query string
			args  []any
		)
		if root == "" {
			query = `SELECT id, workspace_id, layer, kind, label, props, version, status, created_at, updated_at
				FROM sem_nodes WHERE workspace_id = $1`
			args = []any{workspaceID}
			argIdx := 2
			if layer != "" {
				query += fmt.Sprintf(" AND layer = $%d", argIdx)
				args = append(args, layer)
			}
			query += ` ORDER BY kind`
		} else {
			rootID, err := uuid.Parse(root)
			if err != nil {
				return fmt.Errorf("%w: root must be a valid UUID", domain.ErrInvalidInput)
			}
			query = `
				WITH RECURSIVE subgraph(id, depth) AS (
					SELECT $2::uuid, 0
					UNION
					SELECT CASE WHEN e.src_id = sg.id THEN e.dst_id ELSE e.src_id END,
					       sg.depth + 1
					FROM sem_edges e
					JOIN subgraph sg ON e.src_id = sg.id OR e.dst_id = sg.id
					WHERE sg.depth < $3
					  AND e.workspace_id = $1
					  AND e.valid_to IS NULL
				)
				SELECT n.id, n.workspace_id, n.layer, n.kind, n.label, n.props,
				       n.version, n.status, n.created_at, n.updated_at
				FROM sem_nodes n
				JOIN (SELECT id, MIN(depth) AS depth FROM subgraph GROUP BY id) sg ON sg.id = n.id
				WHERE n.workspace_id = $1`
			args = []any{workspaceID, rootID, depth}
			if layer != "" {
				query += ` AND n.layer = $4`
				args = append(args, layer)
			}
			query += ` ORDER BY sg.depth, n.kind`
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list nodes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var n domain.SemNode
			if err := rows.Scan(&n.ID, &n.WorkspaceID, &n.Layer, &n.Kind, &n.Label,
				&n.Props, &n.Version, &n.Status, &n.CreatedAt, &n.UpdatedAt); err != nil {
				return fmt.Errorf("scan node: %w", err)
			}
			nodes = append(nodes, &n)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate nodes: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// Update modifies a node by its UUID. Returns ErrForbidden if the node is approved.
func (r *NodeRepository) Update(ctx context.Context, id uuid.UUID, input domain.UpdateNodeInput) (*domain.SemNode, error) {
	var updated domain.SemNode
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		// Check current status and lock row.
		var status string
		err := tx.QueryRow(ctx, `
			SELECT status FROM sem_nodes WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID).Scan(&status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("check node status: %w", err)
		}
		if domain.ImmutableStatuses[status] {
			return domain.ErrForbidden
		}

		// Build dynamic update query. WHERE params = $1, $2; SET params follow.
		var setClauses []string
		var setArgs []any
		argIdx := 3
		if input.Kind != "" {
			setClauses = append(setClauses, fmt.Sprintf("kind = $%d", argIdx))
			setArgs = append(setArgs, input.Kind)
			argIdx++
		}
		if input.Label != "" {
			setClauses = append(setClauses, fmt.Sprintf("label = $%d", argIdx))
			setArgs = append(setArgs, input.Label)
			argIdx++
		}
		if input.Props != nil {
			setClauses = append(setClauses, fmt.Sprintf("props = $%d", argIdx))
			setArgs = append(setArgs, input.Props)
		}
		setClauses = append(setClauses, "updated_at = now()")

		// Args order: $1=id, $2=workspaceID, $3...=set values.
		args := append([]any{id, workspaceID}, setArgs...)
		query := fmt.Sprintf("UPDATE sem_nodes SET %s WHERE id = $1 AND workspace_id = $2",
			joinStrings(setClauses, ", "))
		_, err = tx.Exec(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("update node: %w", err)
		}
		updated, err = scanNode(tx.QueryRow(ctx,
			`SELECT id, workspace_id, layer, kind, label, props, version, status, created_at, updated_at
			 FROM sem_nodes WHERE id = $1 AND workspace_id = $2`, id, workspaceID))
		return err
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// scanNode scans a single node row into a SemNode.
func scanNode(row interface{ Scan(...any) error }) (domain.SemNode, error) {
	var n domain.SemNode
	err := row.Scan(&n.ID, &n.WorkspaceID, &n.Layer, &n.Kind, &n.Label,
		&n.Props, &n.Version, &n.Status, &n.CreatedAt, &n.UpdatedAt)
	return n, err
}

// joinStrings concatenates strings with a separator.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
