package melody

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	StatusNormal = uint32(1)
	StatusStop   = uint32(2)
)

// Session wrapper around websocket connections.
type Session struct {
	Request      *http.Request
	Keys         sync.Map
	conn         *websocket.Conn
	output       chan *envelope
	melody       *Melody
	status       uint32
	rwMutex      *sync.RWMutex
	lastReadTime time.Time
}

func (s *Session) writeMessage(message *envelope) {
	if s.closed() {
		s.melody.errorHandler(s, errors.New("tried to write to closed a session"))
		return
	}
	defer func() {
		if recover() != nil {
			s.melody.errorHandler(s, errors.New("tried to write to closed a session"))
		}
	}()
	s.output <- message
}

func (s *Session) writeRaw(message *envelope) error {
	if s.closed() {
		return errors.New("tried to write to a closed session")
	}

	_ = s.conn.SetWriteDeadline(time.Now().Add(s.melody.Config.WriteWait))
	err := s.conn.WriteMessage(message.t, message.msg)

	if err != nil {
		return err
	}

	return nil
}

func (s *Session) closed() bool {
	return atomic.LoadUint32(&s.status) == StatusStop
}

func (s *Session) close() {
	if !s.closed() {
		s.rwMutex.Lock()
		atomic.StoreUint32(&s.status, StatusStop)
		_ = s.conn.Close()
		close(s.output)
		s.rwMutex.Unlock()
	}
}

func (s *Session) ping() {
	_ = s.writeRaw(&envelope{t: websocket.PingMessage, msg: []byte{}})
}

func (s *Session) writePump() {
	ticker := time.NewTicker(s.melody.Config.PingPeriod)
	defer ticker.Stop()
	for {
		select {
		case msg, ok := <-s.output:
			if !ok {
				return
			}

			err := s.writeRaw(msg)

			if err != nil {
				s.melody.errorHandler(s, err)
				return
			}

			if msg.t == websocket.CloseMessage {
				return
			}

			if msg.t == websocket.TextMessage {
				s.melody.messageSentHandler(s, msg.msg)
			}

			if msg.t == websocket.BinaryMessage {
				s.melody.messageSentHandlerBinary(s, msg.msg)
			}
		case <-ticker.C:
			s.ping()
		}
	}
}

func (s *Session) readPump() {
	s.conn.SetReadLimit(s.melody.Config.MaxMessageSize)
	s.setReadDeadline()

	s.conn.SetPongHandler(func(string) error {
		s.setReadDeadline()
		s.melody.pongHandler(s)
		return nil
	})

	if s.melody.closeHandler != nil {
		s.conn.SetCloseHandler(func(code int, text string) error {
			return s.melody.closeHandler(s, code, text)
		})
	}

	for {
		t, message, err := s.conn.ReadMessage()

		if err != nil {
			s.melody.errorHandler(s, err)
			break
		}
		s.setReadDeadline()
		if t == websocket.TextMessage {
			s.melody.messageHandler(s, message)
		}

		if t == websocket.BinaryMessage {
			s.melody.messageHandlerBinary(s, message)
		}
	}
}

func (s *Session) setReadDeadline() {
	now := time.Now()
	if now.Sub(s.lastReadTime) >= time.Second {
		s.lastReadTime = now
		s.conn.SetReadDeadline(s.lastReadTime.Add(s.melody.Config.PongWait + s.melody.Config.PingPeriod))
	}
}

// Write writes message to session.
func (s *Session) Write(msg []byte) error {
	if s.closed() {
		return errors.New("session is closed")
	}

	s.writeMessage(&envelope{t: websocket.TextMessage, msg: msg})

	return nil
}

// WriteBinary writes a binary message to session.
func (s *Session) WriteBinary(msg []byte) error {
	if s.closed() {
		return errors.New("session is closed")
	}

	s.writeMessage(&envelope{t: websocket.BinaryMessage, msg: msg})

	return nil
}

// Close closes session.
func (s *Session) Close() error {
	if s.closed() {
		return errors.New("session is already closed")
	}

	s.writeMessage(&envelope{t: websocket.CloseMessage, msg: []byte{}})

	return nil
}

// CloseWithMsg closes the session with the provided payload.
// Use the FormatCloseMessage function to format a proper close message payload.
func (s *Session) CloseWithMsg(msg []byte) error {
	if s.closed() {
		return errors.New("session is already closed")
	}

	s.writeMessage(&envelope{t: websocket.CloseMessage, msg: msg})

	return nil
}

// Set is used to store a new key/value pair exclusivelly for this session.
// It also lazy initializes s.Keys if it was not used previously.
func (s *Session) Set(key string, value interface{}) {
	s.Keys.Store(key, value)
}

// Get returns the value for the given key, ie: (value, true).
// If the value does not exists it returns (nil, false)
func (s *Session) Get(key string) (value interface{}, exists bool) {
	return s.Keys.Load(key)
}

// MustGet returns the value for the given key if it exists, otherwise it panics.
func (s *Session) MustGet(key string) interface{} {
	if value, exists := s.Get(key); exists {
		return value
	}

	panic("Key \"" + key + "\" does not exist")
}

// IsClosed returns the status of the connection.
func (s *Session) IsClosed() bool {
	return s.closed()
}

func (s *Session) WriteControl(messageType int, data []byte, deadline time.Time) error {
	return s.conn.WriteControl(messageType, data, deadline)
}

func (s *Session) GetConn() *websocket.Conn {
	return s.conn
}
