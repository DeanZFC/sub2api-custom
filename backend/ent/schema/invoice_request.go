package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InvoiceRequest stores an immutable application snapshot and its processing result.
type InvoiceRequest struct{ ent.Schema }

func (InvoiceRequest) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "payment_invoice_requests"}}
}

func (InvoiceRequest) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id").Immutable(),
		field.String("user_email").MaxLen(255).Immutable(),
		field.JSON("order_ids", []int64{}).Immutable(),
		field.Float("amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).Immutable(),
		field.Float("fee_amount").SchemaType(map[string]string{dialect.Postgres: "decimal(20,2)"}).Default(0).Immutable(),
		field.Bool("fee_refunded").Default(false),
		field.String("currency").MaxLen(3).Default("CNY").Immutable(),
		field.String("buyer_type").MaxLen(20).Immutable(),
		field.String("title").MaxLen(200).Immutable(),
		field.String("tax_id").MaxLen(30).Default("").Immutable(),
		field.JSON("emails", []string{}).Immutable(),
		field.String("remark").MaxLen(2000).Default("").Immutable(),
		field.String("status").MaxLen(20).Default("pending"),
		field.String("admin_remark").MaxLen(2000).Default(""),
		field.String("invoice_number").MaxLen(100).Default(""),
		field.String("file_name").MaxLen(255).Default(""),
		field.Bytes("pdf_data").Optional().Sensitive(),
		field.Time("issued_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (InvoiceRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at"), index.Fields("status", "created_at")}
}
