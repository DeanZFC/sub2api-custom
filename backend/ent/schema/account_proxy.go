package schema

import ("entgo.io/ent"; "entgo.io/ent/schema/field")

// AccountProxy stores the ordered proxy pool assigned to an account. The
// account's existing proxy_id remains the single-proxy compatibility path.
type AccountProxy struct { ent.Schema }

func (AccountProxy) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.Int64("proxy_id"),
		field.Int("position").Default(0),
	}
}
