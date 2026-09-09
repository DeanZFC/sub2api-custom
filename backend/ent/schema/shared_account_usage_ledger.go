package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

type SharedAccountUsageLedger struct{ ent.Schema }

func (SharedAccountUsageLedger) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shared_account_usage_ledger"}}
}
func (SharedAccountUsageLedger) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (SharedAccountUsageLedger) Fields() []ent.Field {
	return []ent.Field{
		field.String("request_id").MaxLen(255).Unique(), field.Int64("usage_log_id").Optional().Nillable().Unique(), field.Int64("listing_id"),
		field.Int64("owner_user_id"), field.Int64("consumer_user_id"), field.Float("gross_cost").SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.Float("fee_rate_percent").SchemaType(map[string]string{dialect.Postgres: "decimal(7,4)"}), field.Float("platform_fee").SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}), field.Float("owner_amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}),
		field.String("action").MaxLen(20).Default("earn"), field.Time("frozen_until").Optional().Nillable(),
		field.Time("released_at").Optional().Nillable(),
	}
}
