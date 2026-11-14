package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type Account struct{ ent.Schema }

func (Account) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_accounts"}}
}
func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.String("name").NotEmpty(), field.String("name_key").NotEmpty().Unique(),
		field.String("password_hash").Sensitive(), field.String("pin_hash").Default("").Sensitive(),
		field.Int("gender").Default(255), field.Time("created_at").Default(time.Now).Immutable(),
	}
}
