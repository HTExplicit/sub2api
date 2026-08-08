package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// SystemPromptTemplate is an administrator-managed business prompt template.
type SystemPromptTemplate struct {
	ent.Schema
}

func (SystemPromptTemplate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "system_prompt_templates"}}
}

func (SystemPromptTemplate) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}, mixins.SoftDeleteMixin{}}
}

func (SystemPromptTemplate) Fields() []ent.Field {
	return []ent.Field{
		field.String("slug").MaxLen(100).NotEmpty().Unique(),
		field.String("name").MaxLen(200).NotEmpty(),
		field.String("description").SchemaType(map[string]string{dialect.Postgres: "text"}).Default(""),
		field.Bool("is_seed").Default(false),
		field.String("managed_source").MaxLen(100).Optional().Nillable(),
		field.Int64("created_by").Optional().Nillable(),
		field.Int64("updated_by").Optional().Nillable(),
	}
}

func (SystemPromptTemplate) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("versions", SystemPromptTemplateVersion.Type).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}
