package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"sync"
	"time"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // Allow all origins for testing
}

type Player struct {
	ID       string    `json:"id"`
	X        float64   `json:"x"`
	Y        float64   `json:"y"`
	LastSeen time.Time `json:"-"` // Track when we last heard from player
}

type MoveMessage struct {
	Type     string  `json:"type"`
	PlayerID string  `json:"playerId"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

type PlayerStore struct {
	players map[*websocket.Conn]Player
	conns   map[string]*websocket.Conn // Map player IDs to connections
	mu      sync.Mutex
}

var store = PlayerStore{
	players: make(map[*websocket.Conn]Player),
	conns:   make(map[string]*websocket.Conn),
}
var broadcast = make(chan MoveMessage)

// Generate a secure random ID
func generateSecureID() string {
	bytes := make([]byte, 4) // 8 hex characters
	_, err := rand.Read(bytes)
	if err != nil {
		log.Printf("Error generating secure ID: %v", err)
		// Fallback to timestamp-based ID
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(bytes)
}

func (ps *PlayerStore) Add(ws *websocket.Conn, player Player) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	// First check if this player ID already exists and clean it up
	if existingConn, found := ps.conns[player.ID]; found {
		log.Printf("Player %s already exists, removing old connection", player.ID)
		delete(ps.players, existingConn)
		existingConn.Close()
	}

	ps.players[ws] = player
	ps.conns[player.ID] = ws

	log.Printf("Added player %s. Total players: %d", player.ID, len(ps.players))
}

func (ps *PlayerStore) Update(ws *websocket.Conn, x, y float64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if player, ok := ps.players[ws]; ok {
		player.X = x
		player.Y = y
		player.LastSeen = time.Now()
		ps.players[ws] = player

		if x != 0 || y != 0 {
			log.Printf("Updated player %s position to (%.2f, %.2f)", player.ID, x, y)
		}
	} else {
		log.Printf("Warning: Tried to update non-existent player")
	}
}

func (ps *PlayerStore) Delete(ws *websocket.Conn) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	if player, ok := ps.players[ws]; ok {
		log.Printf("Removing player %s", player.ID)
		delete(ps.conns, player.ID)
		delete(ps.players, ws)
		log.Printf("Player %s disconnected, total players: %d", player.ID, len(ps.players))
	} else {
		log.Printf("Warning: Tried to delete unknown player connection")
	}
}

func (ps *PlayerStore) GetAllPlayers() []Player {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	players := make([]Player, 0, len(ps.players))
	for _, player := range ps.players {
		players = append(players, player)
	}
	return players
}

func (ps *PlayerStore) Range(f func(ws *websocket.Conn, player Player) bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for ws, player := range ps.players {
		if !f(ws, player) {
			break
		}
	}
}

func (ps *PlayerStore) CleanupInactivePlayers() {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	now := time.Now()
	timeout := 30 * time.Second

	for ws, player := range ps.players {
		if now.Sub(player.LastSeen) > timeout {
			log.Printf("Removing inactive player %s", player.ID)
			delete(ps.conns, player.ID)
			delete(ps.players, ws)
			ws.Close()
		}
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	// Set reasonable timeouts
	ws.SetReadDeadline(time.Now().Add(120 * time.Second))
	ws.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// Register new player with a secure ID
	playerID := generateSecureID()
	player := Player{
		ID:       playerID,
		X:        0,
		Y:        0,
		LastSeen: time.Now(),
	}
	store.Add(ws, player)

	// Send initial state to client
	err = ws.WriteJSON(player)
	if err != nil {
		log.Printf("Error sending initial state: %v", err)
		store.Delete(ws)
		ws.Close()
		return
	}
	log.Printf("New player connected: %s, total players: %d", playerID, len(store.players))

	// Send all existing players to the new player
	allPlayers := store.GetAllPlayers()
	log.Printf("Sending %d existing players to new player %s", len(allPlayers), playerID)

	for _, otherPlayer := range allPlayers {
		// Don't send the player their own info
		if otherPlayer.ID == playerID {
			continue
		}

		msg := MoveMessage{
			Type:     "move",
			PlayerID: otherPlayer.ID,
			X:        otherPlayer.X,
			Y:        otherPlayer.Y,
		}

		// Convert to JSON for logging
		msgBytes, _ := json.Marshal(msg)
		log.Printf("Sending existing player to new player: %s", string(msgBytes))

		err := ws.WriteJSON(msg)
		if err != nil {
			log.Printf("Error sending existing player data: %v", err)
		}
	}

	// Close the connection when this function returns
	defer func() {
		ws.Close()
		store.Delete(ws)
	}()

	// Handle incoming messages
	for {
		// Reset read deadline for each message
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))

		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Error reading message: %v", err)
			}
			break
		}

		var msg MoveMessage
		err = json.Unmarshal(msgBytes, &msg)
		if err != nil {
			log.Printf("Error parsing message: %v, raw message: %s", err, string(msgBytes))
			continue
		}

		log.Printf("Received message from %s: %s", playerID, string(msgBytes))

		// Ensure the message contains the correct player ID
		// This prevents player ID spoofing
		if msg.Type == "move" {
			// Override the player ID with the one we assigned
			msg.PlayerID = playerID

			// Update player position
			store.Update(ws, msg.X, msg.Y)

			// Send to broadcast channel
			broadcast <- msg
		} else {
			log.Printf("Ignoring message with unknown type: %s", msg.Type)
		}
	}
}

func handleBroadcast() {
	for msg := range broadcast {
		playerCount := 0
		sentCount := 0

		store.Range(func(ws *websocket.Conn, player Player) bool {
			playerCount++

			// Don't send message back to originator
			if player.ID == msg.PlayerID {
				return true // continue the range loop
			}

			// Set write deadline for sending
			ws.SetWriteDeadline(time.Now().Add(5 * time.Second))

			// Log the broadcast
			log.Printf("Broadcasting movement of player %s to player %s: (%.2f, %.2f)",
				msg.PlayerID, player.ID, msg.X, msg.Y)

			err := ws.WriteJSON(msg)
			if err != nil {
				log.Printf("Error broadcasting to %s: %v", player.ID, err)
			} else {
				sentCount++
			}

			return true // continue the range loop
		})

		log.Printf("Broadcast complete: sent to %d out of %d players", sentCount, playerCount-1)
	}
}

func startCleanupTask() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		<-ticker.C
		store.CleanupInactivePlayers()
	}
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Simple status page
	fmt.Fprintf(w, "<html><body>")
	fmt.Fprintf(w, "<h1>Game Server Status</h1>")
	fmt.Fprintf(w, "<p>Connected players: %d</p>", len(store.players))
	fmt.Fprintf(w, "<table border='1'><tr><th>ID</th><th>Position</th><th>Last Seen</th></tr>")

	store.Range(func(ws *websocket.Conn, player Player) bool {
		fmt.Fprintf(w, "<tr><td>%s</td><td>(%.2f, %.2f)</td><td>%s</td></tr>",
			player.ID, player.X, player.Y, time.Since(player.LastSeen))
		return true
	})

	fmt.Fprintf(w, "</table></body></html>")
}

func main() {
	http.HandleFunc("/game", handleConnections)
	http.HandleFunc("/status", statusHandler)

	// Start background tasks
	go handleBroadcast()
	go startCleanupTask()

	log.Println("Game server starting on :8080")
	log.Println("View status at http://localhost:8080/status")
	log.Println("Press Ctrl+C to stop the server")

	err := http.ListenAndServe("0.0.0.0:8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
