package notify

type SSEDispatcher struct {
	broker interface {
		Publish(userID, data string)
	}
}

func NewSSEDispatcher(broker interface{ Publish(string, string) }) *SSEDispatcher {
	return &SSEDispatcher{broker: broker}
}

func (d *SSEDispatcher) Publish(userID, jsonData string) {
	if d == nil || d.broker == nil {
		return
	}
	d.broker.Publish(userID, jsonData)
}
