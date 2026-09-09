package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// SharedAccountListing is the user-facing publication of an execution account.
type SharedAccountListing struct{ ent.Schema }

func (SharedAccountListing) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shared_account_listings"}}
}
func (SharedAccountListing) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}, mixins.SoftDeleteMixin{}}
}
func (SharedAccountListing) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("owner_user_id"), field.Int64("account_id").Unique(),
		field.String("platform").MaxLen(50).NotEmpty(), field.String("display_name").MaxLen(100).NotEmpty(),
		field.String("status").MaxLen(20).Default("testing"), field.String("proxy_config_encrypted").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Int("concurrency_limit").Default(1), field.Float("concurrency_multiplier").SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}).Default(1),
		field.Float("sell_rate").SchemaType(map[string]string{dialect.Postgres: "decimal(10,4)"}).Default(1), field.Float("fee_rate_override").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "decimal(7,4)"}),
		field.Int64("total_call_count").Default(0), field.Time("last_called_at").Optional().Nillable(),
	}
}
func (SharedAccountListing) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_user_id", "status"), index.Fields("platform", "status")}
}
