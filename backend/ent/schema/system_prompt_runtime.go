package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// SystemPromptRuntime is the singleton global activation policy.
type SystemPromptRuntime struct {
	ent.Schema
}

func (SystemPromptRuntime) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "system_prompt_runtime"}}
}

func (SystemPromptRuntime) Fields() []ent.Field {
	return []ent.Field{
		field.Bool("enabled").Default(false),
		field.Bool("expose_server_prompt").Default(false),
		field.Bool("compact_enabled").Default(false),
		field.Int64("active_template_id").Optional().Nillable(),
		field.Int64("active_version_id").Optional().Nillable(),
		field.Int64("revision").Default(1),
		field.Int64("updated_by").Optional().Nillable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (SystemPromptRuntime) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("active_template", SystemPromptTemplate.Type).
			Field("active_template_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("active_version", SystemPromptTemplateVersion.Type).
			Field("active_version_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}
