// Package pirate registers the skills of 海盜＋重砲 job 500–532 (thread P).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "pirate" may be registered here.
package pirate
