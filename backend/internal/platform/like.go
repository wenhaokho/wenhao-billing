package platform

import "strings"

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// LikeEscape makes s literal inside a LIKE or ILIKE pattern whose escape
// character is a backslash (the queries write ESCAPE '\'): a backslash, % and
// _ each get a backslash in front. Every substring search that takes text
// from a user (the payments audit log's payer, the MCP name filters) passes
// it through here.
func LikeEscape(s string) string { return likeEscaper.Replace(s) }
