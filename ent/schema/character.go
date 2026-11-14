package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Character struct{ ent.Schema }

func (Character) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "game_characters"}}
}
func (Character) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("account_id"), field.Int("world_id"), field.String("name"), field.String("name_key").Unique(),
		field.String("operation_id").Unique(), field.Int("gender"), field.Int("skin"), field.Int("face"), field.Int("hair"),
		field.Int("level").Default(1), field.Int("job").Default(0),
		field.Int("str").Default(12), field.Int("dex").Default(5), field.Int("int_stat").Default(4), field.Int("luk").Default(4),
		field.Int("hp").Default(50), field.Int("mp").Default(5), field.Int("max_hp").Default(50), field.Int("max_mp").Default(5),
		field.Int("ap").Default(0), field.Int("sp").Default(0), field.Int64("exp").Default(0), field.Int("fame").Default(0), field.Int64("mesos").Default(0),
		field.Int("map_id").Default(10000), field.Int("spawn_point").Default(0), field.Int("x").Default(-192), field.Int("y").Default(210),
		field.Int("gm").Default(0), field.Int64("revision").Default(1),
	}
}
func (Character) Indexes() []ent.Index { return []ent.Index{index.Fields("account_id", "world_id")} }
