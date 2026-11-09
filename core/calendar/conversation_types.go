package calendar

// ChatEvidence contains only the referenced public message and its conversation.
// The raw transcript and tool payloads are never part of the calendar database.
type ChatEvidence struct {
	ID          string `json:"id"`
	ThreadID    string `json:"threadId"`
	ThreadTitle string `json:"threadTitle"`
	At          int64  `json:"at"`
	Quote       string `json:"quote"`
}

type ChatChange struct {
	Source      ChatEvidence `json:"source"`
	TaskKey     string       `json:"taskKey"`
	EventID     int64        `json:"eventId"`
	Title       string       `json:"title"`
	Phase       string       `json:"phase"`
	Description string       `json:"description"`
	Result      string       `json:"result"`
	NeedsReview bool         `json:"needsReview"`
	BaseUpdated string       `json:"baseUpdated"`
}

type ChatActivity struct {
	Current     *Event     `json:"current,omitempty"`
	ID          int64      `json:"id"`
	Day         string     `json:"day"`
	State       string     `json:"state"` // pending, applied, dismissed
	Reason      string     `json:"reason"`
	BeforePhase string     `json:"beforePhase"`
	Change      ChatChange `json:"change"`
}

type ChatCounts struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Pending int `json:"pending"`
	Skipped int `json:"skipped"`
}
