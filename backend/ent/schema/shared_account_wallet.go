package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

type SharedAccountWallet struct{ ent.Schema }

func (SharedAccountWallet) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shared_account_wallets"}}
}
func (SharedAccountWallet) Mixin() []ent.Mixin { return []ent.Mixin{mixins.TimeMixin{}} }
func (SharedAccountWallet) Fields() []ent.Field {
	n := func(name string) ent.Field {
		return field.Float(name).SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).Default(0)
	}
	return []ent.Field{field.Int64("user_id").Unique(), n("pending_amount"), n("available_amount"), n("frozen_amount"), n("total_earned"), n("total_transferred")}
}
