package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Equipment struct{ ent.Schema }

func (Equipment) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "asset_equipment"}}
}
func (Equipment) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("character_id"), field.Int("item_id"), field.Int("slot"),
		field.Int("str").Default(0), field.Int("dex").Default(0), field.Int("int_stat").Default(0), field.Int("luk").Default(0),
		field.Int("hp").Default(0), field.Int("mp").Default(0), field.Int("watk").Default(0), field.Int("matk").Default(0),
		field.Int("wdef").Default(0), field.Int("mdef").Default(0), field.Int("acc").Default(0), field.Int("avoid").Default(0),
		field.Int("speed").Default(0), field.Int("jump").Default(0), field.Int("upgrade_slots").Default(7),
	}
}
func (Equipment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("character_id", "slot").Unique()}
}
