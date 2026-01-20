package web

// buildAgentRequest constructs the payload for agent task submission.
func buildAgentRequest(prompt, model, tier string, timeoutSeconds int, sessionID string, env map[string]string, thinking *bool) map[string]interface{} {
	req := map[string]interface{}{
		"prompt": prompt,
	}
	if model != "" {
		req["model"] = model
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
	if thinking != nil {
		req["thinking"] = *thinking
	}
	return req
}
