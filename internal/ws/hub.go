package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Client struct {
	Hub     *Hub
	Conn    *websocket.Conn
	Send    chan []byte
	TeamPIN string
	UserID  uint
	Role    string // "monitor" ou "player"
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[*Client]bool
	// Callback opcional chamado quando a presença online de uma sala muda
	OnPresenceChange func(teamPIN string)
}

func NewHub() *Hub {
	return &Hub{
		rooms: make(map[string]map[*Client]bool),
	}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	if h.rooms[c.TeamPIN] == nil {
		h.rooms[c.TeamPIN] = make(map[*Client]bool)
	}
	h.rooms[c.TeamPIN][c] = true
	h.mu.Unlock()

	if h.OnPresenceChange != nil {
		h.OnPresenceChange(c.TeamPIN)
	}
}

func (h *Hub) Unregister(c *Client) {
	removed := false
	h.mu.Lock()
	if clients, ok := h.rooms[c.TeamPIN]; ok {
		if _, exists := clients[c]; exists {
			delete(clients, c)
			close(c.Send)
			removed = true
			if len(clients) == 0 {
				delete(h.rooms, c.TeamPIN)
			}
		}
	}
	h.mu.Unlock()

	if removed && h.OnPresenceChange != nil {
		h.OnPresenceChange(c.TeamPIN)
	}
}

// GetOnlineUsers retorna um mapa com os IDs dos usuários conectados via WebSocket na sala.
func (h *Hub) GetOnlineUsers(teamPIN string) map[uint]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	online := make(map[uint]bool)
	if clients, ok := h.rooms[teamPIN]; ok {
		for c := range clients {
			if c.UserID > 0 {
				online[c.UserID] = true
			}
		}
	}
	return online
}

// Broadcast envia uma mensagem JSON para todos os clientes (Monitor e Celulares) conectados na sala.
func (h *Hub) Broadcast(teamPIN string, eventType string, data any) {
	payload, err := json.Marshal(map[string]any{
		"type": eventType,
		"data": data,
	})
	if err != nil {
		log.Printf("Erro ao serializar broadcast WS: %v", err)
		return
	}

	h.mu.RLock()
	clients := h.rooms[teamPIN]
	var targets []*Client
	for c := range clients {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.Send <- payload:
		default:
			h.Unregister(c)
		}
	}
}

func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request, teamPIN string, userID uint, role string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Erro no upgrade WebSocket: %v", err)
		return
	}

	client := &Client{
		Hub:     hub,
		Conn:    conn,
		Send:    make(chan []byte, 64),
		TeamPIN: teamPIN,
		UserID:  userID,
		Role:    role,
	}

	hub.Register(client)

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.Unregister(c)
		c.Conn.Close()
	}()
	c.Conn.SetReadLimit(4096)
	_ = c.Conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		return nil
	})
	for {
		_, _, err := c.Conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
