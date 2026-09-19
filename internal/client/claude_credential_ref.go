package client

import "strings"

func validClaudeCredentialRef(ref string) bool {
	id, ok := strings.CutSuffix(ref, ".token")
	return ok && uuidLike(id) && id == strings.ToLower(id)
}
