package notify

type Dispatcher interface {
	Publish(userID, jsonData string)
}
