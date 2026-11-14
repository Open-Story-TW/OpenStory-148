// Package bowman registers the skills of 弓箭手 job 300–322 (thread B).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "bowman" may be registered here.
package bowman
