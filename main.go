package main

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"strconv"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // Allow all origins for testing
}

type Player struct {
	ID string  `json:"id"`
	X  float32 `json:"x"`
	Y  float32 `json:"y"`
}

type MoveMessage struct {
	Type     string  `json:"type"`
	PlayerID string  `json:"playerId"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
}

var players = make(map[*websocket.Conn]Player)
var broadcast = make(chan MoveMessage)

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	// Register new player
	playerID := generateID()
	player := Player{ID: playerID, X: 0, Y: 0}
	players[ws] = player

	// Send initial state to client
	err = ws.WriteJSON(player)
	if err != nil {
		log.Printf("error sending initial state: %v", err)
		return
	}
	log.Printf("New player connected: %s", playerID)

	// Handle incoming messages
	for {
		_, msgBytes, err := ws.ReadMessage()
		if err != nil {
			log.Printf("error reading message: %v", err)
			delete(players, ws)
			break
		}

		var msg MoveMessage
		err = json.Unmarshal(msgBytes, &msg)
		if err != nil {
			log.Printf("error parsing message: %v", err)
			continue
		}

		log.Printf("Received message: %+v", msg)

		// Update player position
		if msg.Type == "move" {
			player := players[ws]
			player.X = msg.X
			player.Y = msg.Y
			players[ws] = player

			// Create broadcast message
			broadcastMsg := MoveMessage{
				Type:     "move",
				PlayerID: player.ID,
				X:        player.X,
				Y:        player.Y,
			}

			broadcast <- broadcastMsg
		}
	}
}

func handleBroadcast() {
	for msg := range broadcast {
		for ws, player := range players {
			// Don't send message back to originator
			if player.ID == msg.PlayerID {
				continue
			}

			err := ws.WriteJSON(msg)
			if err != nil {
				log.Printf("error broadcasting: %v", err)
				ws.Close()
				delete(players, ws)
			}
		}
	}
}

func generateID() string {
	return "player-" + strconv.Itoa(len(players)+1)
}

func main() {
	http.HandleFunc("/game", handleConnections)
	go handleBroadcast()

	log.Println("Server starting on :8080")
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
