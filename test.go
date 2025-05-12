package main

import (
	"github.com/gorilla/websocket"
	"sync"
)

type TestMap struct {
	Group map[string]map[string]*websocket.Conn
	sync.Mutex
}

func (ts *TestMap) test() {
	for k, v := range ts.Group {
		for k1, v1 := range v {
			println(k, k1, v1)
		}
	}
}

func (ts *TestMap) Add(idGroup string, idUser string, ws *websocket.Conn) {
	ts.Lock()
	if _, ok := ts.Group[idGroup]; !ok {
		ts.Group[idGroup] = make(map[string]*websocket.Conn)
	}
	ts.Group[idGroup][idUser] = ws

	defer ts.Unlock()
}
