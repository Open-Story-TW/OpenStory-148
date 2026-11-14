// Package magician registers the skills of 法師 job 200–232 (thread M).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "magician" may be registered here.
package magician
