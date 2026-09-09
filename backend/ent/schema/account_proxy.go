package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// AccountProxy is the ordered set of outbound proxies for an account.
// Single-proxy accounts continue using accounts.proxy_id without a pool row.
type AccountProxy struct{ ent.Schema }

func (AccountProxy) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "account_proxies"}, field.ID("account_id", "proxy_id")}
}
func (AccountProxy) Fields() []ent.Field {
	return []ent.Field{field.Int64("account_id"), field.Int64("proxy_id"), field.Int("position").Default(0), field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"})}
}
func (AccountProxy) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).Field("account_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("proxy", Proxy.Type).Field("proxy_id").Unique().Required().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
func (AccountProxy) Indexes() []ent.Index {
	return []ent.Index{index.Fields("proxy_id").StorageKey("idx_account_proxies_proxy_id"), index.Fields("account_id", "position").StorageKey("idx_account_proxies_account_position")}
}
