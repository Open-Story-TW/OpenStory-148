package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type Outbox struct{ ent.Schema }

func (Outbox) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ops_outbox"}}
}
func (Outbox) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.String("event_type"), field.Int64("account_id"), field.Int("world_id"),
		field.Time("created_at").Default(time.Now), field.Time("processed_at").Optional().Nillable(),
	}
}
