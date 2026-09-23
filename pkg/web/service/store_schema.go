package service

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/uptrace/bun"
)

// SchemaModule is one optional slice of the store schema. The core schema is
// always installed; a host adds modules (e.g. ScanSchema) at open time and the
// database must match the exact union — there are no migrations.
type SchemaModule struct {
	Name    string
	Models  []any
	Indexes []SchemaIndex
	Tables  map[string][]string
}

// SchemaIndex is one declarative index. Expr carries expression indexes such
// as "created_at DESC"; otherwise Columns are indexed in order.
type SchemaIndex struct {
	Model   any
	Name    string
	Columns []string
	Expr    string
	Unique  bool
}

// createTableCustomizer lets a model declare constraints bun cannot infer,
// such as the composite-primary-key junction's foreign keys.
type createTableCustomizer interface {
	customizeCreateTable(query *bun.CreateTableQuery) *bun.CreateTableQuery
}

// coreSchema is the host-neutral session/event/artifact storage every web
// host shares.
var coreSchema = SchemaModule{
	Name: "core",
	Models: []any{
		(*sessionModel)(nil),
		(*aopEventModel)(nil),
		(*requestLedgerModel)(nil),
		(*rawArtifactModel)(nil),
	},
	Indexes: []SchemaIndex{
		{Model: (*sessionModel)(nil), Name: "idx_sessions_updated", Expr: "updated_at DESC"},
		{Model: (*sessionModel)(nil), Name: "idx_sessions_node_id", Columns: []string{"node_id"}},
		{Model: (*aopEventModel)(nil), Name: "idx_aop_events_session", Columns: []string{"session_id", "cursor"}},
		{Model: (*aopEventModel)(nil), Name: "idx_aop_events_turn", Columns: []string{"turn_id"}},
		{Model: (*aopEventModel)(nil), Name: "idx_aop_events_event_id", Columns: []string{"session_id", "event_id"}, Unique: true},
		{Model: (*rawArtifactModel)(nil), Name: "idx_raw_artifacts_created", Columns: []string{"created_at"}},
	},
	Tables: map[string][]string{
		"aop_request_ledger": {"request_id", "method", "request_hash", "response_json", "created_at"},
		"chat_aop_events":    {"id", "session_id", "event_id", "cursor", "turn_id", "emitter", "sequence", "event_json", "created_at"},
		"chat_sessions":      {"id", "node_id", "status", "title", "agent_name", "session_json", "created_at", "updated_at"},
		"raw_artifacts":      {"cursor", "event_id", "event_proto", "created_at"},
	},
}

// schemaUnion joins the core schema with the configured modules.
func schemaUnion(modules []SchemaModule) SchemaModule {
	union := SchemaModule{
		Name:    "core",
		Models:  slices.Clone(coreSchema.Models),
		Indexes: slices.Clone(coreSchema.Indexes),
		Tables:  map[string][]string{},
	}
	for table, columns := range coreSchema.Tables {
		union.Tables[table] = columns
	}
	for _, module := range modules {
		union.Name += "+" + module.Name
		union.Models = append(union.Models, module.Models...)
		union.Indexes = append(union.Indexes, module.Indexes...)
		for table, columns := range module.Tables {
			union.Tables[table] = columns
		}
	}
	return union
}

func (s SchemaModule) tableNames() []string {
	tables := make([]string, 0, len(s.Tables))
	for table := range s.Tables {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	return tables
}

// createSchema installs the union schema into an empty database.
func createSchema(ctx context.Context, orm *bun.DB, schema SchemaModule) error {
	return orm.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, model := range schema.Models {
			query := tx.NewCreateTable().Model(model).WithForeignKeys()
			if custom, ok := model.(createTableCustomizer); ok {
				query = custom.customizeCreateTable(query)
			}
			if _, err := query.Exec(ctx); err != nil {
				return err
			}
		}
		for _, index := range schema.Indexes {
			query := tx.NewCreateIndex().Model(index.Model).Index(index.Name)
			if index.Expr != "" {
				query = query.ColumnExpr(index.Expr)
			} else {
				query = query.Column(index.Columns...)
			}
			if index.Unique {
				query = query.Unique()
			}
			if _, err := query.Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

// validateSchema requires an existing database to match the union exactly.
func validateSchema(db *sql.DB, schema SchemaModule) error {
	tables, err := schemaTables(db)
	if err != nil {
		return err
	}
	if expected := schema.tableNames(); !slices.Equal(tables, expected) {
		return fmt.Errorf("database does not match the latest schema: tables %v, want %v; recreate the database", tables, expected)
	}
	for _, table := range tables {
		columns, err := schemaColumns(db, table)
		if err != nil {
			return err
		}
		if !slices.Equal(columns, schema.Tables[table]) {
			return fmt.Errorf("database does not match the latest schema: %s columns %v, want %v; recreate the database", table, columns, schema.Tables[table])
		}
	}
	return nil
}

func schemaTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func schemaColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info("` + table + `")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}
