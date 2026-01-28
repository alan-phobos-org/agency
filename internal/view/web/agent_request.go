package web

// buildAgentRequest constructs the payload for agent task submission.
func buildAgentRequest(prompt, tier string, timeoutSeconds int, sessionID string, env map[string]string) map[string]any {
	req := map[string]any{
		"prompt": prompt,
	}
	if tier != "" {
		req["tier"] = tier
	}
	if timeoutSeconds > 0 {
		req["timeout_seconds"] = timeoutSeconds
	}
	if sessionID != "" {
		req["session_id"] = sessionID
	}
	if len(env) > 0 {
		req["env"] = env
	}
	return req
}
