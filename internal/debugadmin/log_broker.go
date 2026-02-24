package debugadmin

import "sync"

type LogBroker struct {
	mu      sync.RWMutex
	nextID  int
	clients map[int]chan string
}

func NewLogBroker() *LogBroker {
	return &LogBroker{
		clients: make(map[int]chan string),
	}
}

func (b *LogBroker) Subscribe() (<-chan string, func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan string, 256)
	b.clients[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		client, ok := b.clients[id]
		if ok {
			delete(b.clients, id)
			close(client)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

func (b *LogBroker) Broadcast(message string) {
	b.mu.RLock()
	for _, ch := range b.clients {
		select {
		case ch <- message:
		default:
		}
	}
	b.mu.RUnlock()
}
