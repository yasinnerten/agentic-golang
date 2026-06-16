package loopcontroller

type EventBroadcaster interface {
	BroadcastPipelineEvent(tenantID, sessionID, eventType string, payload map[string]any)
}
