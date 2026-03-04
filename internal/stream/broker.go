package stream

import (
	"sync"
)

type Event struct {
	Type string
	Data []byte
}

type Broker struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]chan Event
}

func NewBroker() *Broker {
	return &Broker{subs: map[int]chan Event{}}
}

func (b *Broker) Subscribe() (int, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++
	ch := make(chan Event, 16)
	b.subs[id] = ch
	return id, ch
}

func (b *Broker) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		delete(b.subs, id)
		close(ch)
	}
}

func (b *Broker) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- evt:
		default:
		}
	}
}
