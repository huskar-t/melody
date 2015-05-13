package melody

import (
	"github.com/gorilla/websocket"
	"time"
)

type Session struct {
	Conn   *websocket.Conn
	output chan *envelope
	config *Config
}

func newSession(config *Config, conn *websocket.Conn) *Session {
	return &Session{
		Conn:   conn,
		output: make(chan *envelope, config.MessageBufferSize),
		config: config,
	}
}

func (s *Session) writeMessage(message *envelope) {
	s.output <- message
}

func (s *Session) writeRaw(message *envelope) error {
	s.Conn.SetWriteDeadline(time.Now().Add(s.config.WriteWait))
	return s.Conn.WriteMessage(message.t, message.msg)
}

func (s *Session) close() {
	s.writeRaw(&envelope{t: websocket.CloseMessage, msg: []byte{}})
}

func (s *Session) ping() {
	s.writeMessage(&envelope{t: websocket.PingMessage, msg: []byte{}})
}

func (s *Session) writePump(errorHandler handleErrorFunc) {
	defer s.Conn.Close()

	ticker := time.NewTicker(s.config.PingPeriod)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-s.output:
			if !ok {
				s.close()
				return
			}
			if err := s.writeRaw(msg); err != nil {
				go errorHandler(s, err)
				return
			}
		case <-ticker.C:
			s.ping()
		}
	}
}

func (s *Session) readPump(messageHandler handleMessageFunc, errorHandler handleErrorFunc) {
	defer s.Conn.Close()

	s.Conn.SetReadLimit(s.config.MaxMessageSize)
	s.Conn.SetReadDeadline(time.Now().Add(s.config.PongWait))

	s.Conn.SetPongHandler(func(string) error {
		s.Conn.SetReadDeadline(time.Now().Add(s.config.PongWait))
		return nil
	})

	for {
		_, message, err := s.Conn.ReadMessage()

		if err != nil {
			go errorHandler(s, err)
			break
		}

		go messageHandler(s, message)
	}
}

func (s *Session) Write(msg []byte) {
	s.writeMessage(&envelope{t: websocket.TextMessage, msg: msg})
}

func (s *Session) Close() {
	s.writeMessage(&envelope{t: websocket.CloseMessage, msg: []byte{}})
}