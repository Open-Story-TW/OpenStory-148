// Package cygnus registers the skills of 皇家騎士團 job 1100–1512 (thread C).
//
// Add one file per skill group and call skills.Register from its init
// function; internal/skills/all already imports this package. Only skill IDs
// for which skills.OwnerOf returns "cygnus" may be registered here.
package cygnus
