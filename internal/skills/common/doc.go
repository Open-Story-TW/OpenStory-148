// Package common registers the skills of 新手家族、GM、騎寵、公會與專業技能 (thread Z).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "common" may be registered here.
package common
