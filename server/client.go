package main

import (
	"net"

	"github.com/google/uuid"
)

type Client struct {
	ID       string
	Username string
	conn     net.Conn
	channel  *Channel
	server   *Server
	send     chan string
}

func NewClient(conn net.Conn, server *Server, name string) *Client {
	return &Client{
		ID:       uuid.NewString(),
		Username: name,
		conn:     conn,
		server:   server,
		send:     make(chan string, 512), // Buffered channel to prevent blocking
	}
}
