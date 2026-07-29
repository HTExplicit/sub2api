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

// AccountTag is a management-only label that can be attached to many accounts.
type AccountTag struct {
	ent.Schema
}

func (AccountTag) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "account_tags"}}
}

func (AccountTag) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (AccountTag) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty(),
		field.String("normalized_name").MaxLen(100).NotEmpty().Unique(),
		field.Int("sort_order").Default(0),
	}
}

func (AccountTag) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("accounts", Account.Type).
			Ref("tags").
			Through("account_tag_bindings", AccountTagBinding.Type),
	}
}

func (AccountTag) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sort_order", "name")}
}
