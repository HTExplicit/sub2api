package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AccountFolder is a flat, management-only account classification. It is not
// consulted by the scheduler, billing, authorization, or model routing paths.
type AccountFolder struct {
	ent.Schema
}

func (AccountFolder) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "account_folders"}}
}

func (AccountFolder) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (AccountFolder) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty(),
		field.String("normalized_name").MaxLen(100).NotEmpty().Unique(),
		field.Int("sort_order").Default(0),
	}
}

func (AccountFolder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("accounts", Account.Type).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (AccountFolder) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sort_order", "name")}
}
