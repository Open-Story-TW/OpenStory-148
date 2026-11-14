package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"maple-go-server/internal/domain"
)

type CharacterRoster struct{ ent.Schema }

func (CharacterRoster) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "game_character_roster"}}
}
func (CharacterRoster) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("account_id"), field.Int("world_id"), field.Int64("character_id").Unique(),
		field.JSON("snapshot", domain.Character{}), field.Int64("revision").Default(1),
	}
}
func (CharacterRoster) Indexes() []ent.Index {
	return []ent.Index{index.Fields("account_id", "world_id")}
}
