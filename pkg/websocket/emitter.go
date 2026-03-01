package websocket

// Emitter is a thin typed wrapper over Hub that accepts *Event values
// instead of raw bytes.
type Emitter struct {
	hub *Hub
}

// NewEmitter creates an Emitter backed by the given Hub.
func NewEmitter(hub *Hub) *Emitter {
	return &Emitter{hub: hub}
}

// ToSession sends an event to a single session.
func (e *Emitter) ToSession(sessionID string, event *Event) {
	e.hub.SendToSession(sessionID, event.JSON())
}

// ToSubject sends an event to all sessions for a subject.
func (e *Emitter) ToSubject(subjectID string, event *Event) {
	e.hub.SendToSubject(subjectID, event.JSON())
}

// ToRoom sends an event to all clients in a room.
func (e *Emitter) ToRoom(room string, event *Event) {
	e.hub.SendToRoom(room, event.JSON())
}

// ToRoomExcept sends an event to all room clients except one session.
func (e *Emitter) ToRoomExcept(room string, event *Event, exceptSessionID string) {
	e.hub.SendToRoomExcept(room, event.JSON(), exceptSessionID)
}

// Broadcast sends an event to every connected client.
func (e *Emitter) Broadcast(event *Event) {
	e.hub.Broadcast(event.JSON())
}
