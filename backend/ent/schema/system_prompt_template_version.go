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

// SystemPromptTemplateVersion stores immutable prompt content and its digest.
type SystemPromptTemplateVersion struct {
	ent.Schema
}

func (SystemPromptTemplateVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "system_prompt_template_versions"}}
}

func (SystemPromptTemplateVersion) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("template_id"),
		field.Int64("version"),
		field.String("body").SchemaType(map[string]string{dialect.Postgres: "text"}).NotEmpty(),
		field.String("sha256").MaxLen(64).MinLen(64).NotEmpty(),
		field.Int("byte_length").Range(1, 65536),
		field.String("note").MaxLen(500).Default(""),
		field.Int64("created_by").Optional().Nillable(),
		field.Time("published_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int64("published_by").Optional().Nillable(),
		field.Time("created_at").Immutable().Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (SystemPromptTemplateVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("template", SystemPromptTemplate.Type).
			Ref("versions").
			Field("template_id").
			Unique().
			Required(),
	}
}

func (SystemPromptTemplateVersion) Indexes() []ent.Index {
	return []ent.Index{index.Fields("template_id", "version").Unique()}
}
