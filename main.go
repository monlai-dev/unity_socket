package main

import (
	_ "encoding/json"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
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

type Message struct {
	Type string  `json:"type"`
	X    float32 `json:"x"`
	Y    float32 `json:"y"`
}

var players = make(map[*websocket.Conn]Player)
var broadcast = make(chan Message)

func handleConnections(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer ws.Close()

	// Register new player
	player := Player{ID: generateID(), X: 0, Y: 0}
	players[ws] = player

	// Send initial state to client
	ws.WriteJSON(player)

	// Handle incoming messages
	for {
		var msg Message
		err := ws.ReadJSON(&msg)
		if err != nil {
			log.Printf("error: %v", err)
			delete(players, ws)
			break
		}

		// Update player position
		if msg.Type == "move" {
			player.X = msg.X
			player.Y = msg.Y
			players[ws] = player
			broadcast <- msg
		}
	}
}

func handleBroadcast() {
	for msg := range broadcast {
		for ws, _ := range players {
			err := ws.WriteJSON(msg)
			if err != nil {
				log.Printf("error: %v", err)
				ws.Close()
				delete(players, ws)
			}
		}
	}
}

func generateID() string {
	return "player-" + string(rune(len(players)+1))
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
