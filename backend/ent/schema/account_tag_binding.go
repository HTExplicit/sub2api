package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountTagBinding is the explicit many-to-many join between accounts and
// management tags.
type AccountTagBinding struct {
	ent.Schema
}

func (AccountTagBinding) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_tag_bindings"},
		field.ID("account_id", "tag_id"),
	}
}

func (AccountTagBinding) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
		field.Int64("tag_id"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AccountTagBinding) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Unique().Required().Field("account_id").
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("tag", AccountTag.Type).
			Unique().Required().Field("tag_id").
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (AccountTagBinding) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tag_id")}
}
