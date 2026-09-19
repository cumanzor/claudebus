package client

// A fork carries history, but joins using the new session's own identity.
func BootstrapPrompt(channel, parentAlias string) string {
	return bootstrapNativePrompt(channel, parentAlias, "")
}

func BootstrapPromptAliased(channel, parentAlias, childAlias string) string {
	return bootstrapNativePrompt(channel, parentAlias, childAlias)
}

func bootstrapNativePrompt(channel, parentAlias, childAlias string) string {
	return "You are a forked Claude Code session on the cbus message bus. " + claudeNativeReceivePrompt(channel, childAlias) +
		" Your parent is '" + channel + "/" + parentAlias + "'; it sees your join through presence, so no manual announcement is needed. " +
		"When your assigned task finishes, send the parent a short result summary. An inherited background-task note belongs to the parent; do not restart its listener."
}
