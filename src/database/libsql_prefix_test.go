package database

import "testing"

func TestMapLibSQLIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "schema and CTE references",
			query: `WITH active AS (SELECT id FROM owners) SELECT * FROM active JOIN api_tokens ON api_tokens.owner_id = active.id`,
			want:  `WITH active AS (SELECT id FROM srv_owners) SELECT * FROM active JOIN srv_api_tokens ON srv_api_tokens.owner_id = active.id`,
		},
		{
			name:  "quoted identifiers",
			query: `SELECT "rate_limits".count FROM [rate_limits] JOIN ` + "`audit_log`" + ` ON 1=1`,
			want:  `SELECT "srv_rate_limits".count FROM [srv_rate_limits] JOIN ` + "`srv_audit_log`" + ` ON 1=1`,
		},
		{
			name:  "string and comments are preserved",
			query: `SELECT 'owners', rate_limits.count FROM rate_limits -- audit_log` + "\n" + `/* api_tokens */`,
			want:  `SELECT 'owners', srv_rate_limits.count FROM srv_rate_limits -- audit_log` + "\n" + `/* api_tokens */`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mapLibSQLIdentifiers(test.query); got != test.want {
				t.Fatalf("mapLibSQLIdentifiers() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMapLibSQLNameOnlyMapsKnownNames(t *testing.T) {
	if got := mapLibSQLName("owners"); got != "srv_owners" {
		t.Fatalf("known name = %q, want srv_owners", got)
	}
	if got := mapLibSQLName("owner_id"); got != "owner_id" {
		t.Fatalf("unknown name = %q, want owner_id", got)
	}
	if got := mapLibSQLName("backups"); got != "srv_backups" {
		t.Fatalf("backup table name = %q, want srv_backups", got)
	}
}
