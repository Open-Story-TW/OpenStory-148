// Package warrior registers the skills of 戰士 job 100–132 (thread W).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "warrior" may be registered here.
package warrior
