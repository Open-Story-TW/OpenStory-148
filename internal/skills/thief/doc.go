// Package thief registers the skills of 盜賊＋影武者 job 400–434 (thread T).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "thief" may be registered here.
package thief
