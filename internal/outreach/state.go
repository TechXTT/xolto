package outreach

type State string

const (
	StateAwaitingReply State = "awaiting_reply"
	StateReplied       State = "replied"
	StateStale         State = "stale"
	StateSent          State = "sent"
)
