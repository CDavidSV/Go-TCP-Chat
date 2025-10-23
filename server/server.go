package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

var (
	maxBucketSize = 10  // Maximum number of tokens in the bucket
	bucketRate    = 1.5 // Tokens per second to refill the bucket
)

type Server struct {
	clients     map[string]*Client // IP address (unregistered) or Username (registered)
	channels    map[string]*Channel
	commands    map[string]CommandFunc
	command     chan Command
	register    chan *Client
	unregister  chan *Client
	setUsername chan UsernameChange
	broadcast   chan Message
	shutdown    chan struct{}
	url         *url.URL
	logger      *slog.Logger
	wg          sync.WaitGroup
	stopped     bool
}

type UsernameChange struct {
	Client      *Client
	OldKey      string
	NewUsername string
	Response    chan error
}

func NewServer(address, port string) *Server {
	url, err := url.Parse("tcp://" + address + ":" + port)
	if err != nil {
		panic("Failed to parse server URL: " + err.Error())
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	server := &Server{
		clients:     make(map[string]*Client),
		channels:    make(map[string]*Channel),
		commands:    make(map[string]CommandFunc),
		command:     make(chan Command),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		setUsername: make(chan UsernameChange),
		broadcast:   make(chan Message, 1024), // Buffered channel to prevent blocking
		shutdown:    make(chan struct{}),
		url:         url,
		logger:      logger,
		stopped:     false,
	}

	server.loadCommands()
	return server
}

func formatMessage(senderID, senderName, content string) string {
	if senderID == "" {
		senderID = "."
	}
	if senderName == "" {
		senderName = "."
	}

	var builder strings.Builder
	builder.Grow(len(senderID) + len(senderName) + len(content) + 2)
	builder.WriteString(senderID)
	builder.WriteByte('|')
	builder.WriteString(senderName)
	builder.WriteByte('|')
	builder.WriteString(content)
	return builder.String()
}

// changeUsername validates and updates a client's username
func (s *Server) changeUsername(client *Client, oldKey, newUsername string) error {
	// Validate username
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return fmt.Errorf("username cannot be empty")
	}

	if len(newUsername) > 32 {
		return fmt.Errorf("username cannot exceed 32 characters")
	}

	if strings.HasPrefix(newUsername, "temp_") {
		return fmt.Errorf("username cannot start with 'temp_'")
	}

	// Check for duplicate usernames
	if existingClient, exists := s.clients[newUsername]; exists && existingClient != client {
		return fmt.Errorf("'%s' is already taken", newUsername)
	}

	// Delete old key from map
	delete(s.clients, oldKey)

	// Add client with new username as key
	s.clients[newUsername] = client
	client.SetUsername(newUsername)

	return nil
}

func (s *Server) run() {
	defer s.wg.Done()

	for {
		select {
		case client := <-s.register:
			// Handle new client registration - use IP address as initial key
			s.clients[client.IP] = client
			s.logger.Info("Client connected", "client_id", client.ID, "ip", client.IP, "total_clients", len(s.clients))
			client.SendMessage(formatMessage("", "Server", "Welcome! Please set your username by typing it in."))

			// Start reader and writer goroutines for the client
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				client.Read()
			}()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				client.Write()
			}()
		case client := <-s.unregister:
			// Handle client unregistration
			clientChannel := client.GetChannel()
			if clientChannel != nil {
				clientChannel.RemoveMember(client)
				s.broadcastMessage(client, clientChannel, fmt.Sprintf("%s has left the channel.", client.GetUsername()), true)
				if len(clientChannel.members) == 0 {
					delete(s.channels, clientChannel.Name)
				}
			}

			// Delete from clients map using username (if registered) or IP (if not)
			if client.IsRegistered() {
				delete(s.clients, client.GetUsername())
			} else {
				delete(s.clients, client.IP)
			}

			close(client.send)
			s.logger.Info("Client disconnected", "client_id", client.ID, "username", client.GetUsername(), "registered", client.IsRegistered(), "ip", client.IP, "total_clients", len(s.clients))
		case usernameChange := <-s.setUsername:
			// Handle username changes from client Read() goroutine
			err := s.changeUsername(usernameChange.Client, usernameChange.OldKey, usernameChange.NewUsername)
			usernameChange.Response <- err
		case cmd := <-s.command:
			// Handle commands from clients
			if cmdFunc, exists := s.commands[cmd.Name]; exists {
				cmdFunc(cmd.Name, cmd.Args, cmd.Client, s) // Execute command if found
			} else {
				cmd.Client.SendMessage(formatMessage("", "Server", "[Server]: Unknown command. Type /help for a list of commands."))
			}
		case msg := <-s.broadcast:
			// If the sender is the server then we don't include the sender ID in the message
			// We still use the id to avoid sending the message back to the sender
			// when broadcasting to a channel or to all clients
			clientSender := msg.SenderID
			if msg.SenderName == "Server" {
				clientSender = ""
			}

			formattedMsg := formatMessage(clientSender, msg.SenderName, msg.Content)

			// Handle broadcasting messages to clients
			if msg.Channel == nil {
				// Broadcast message to all clients if no channel is specified
				for _, client := range s.clients {
					if msg.SenderID != client.ID { // Avoid sending the message back to the sender if any is provided
						client.SendMessage(formattedMsg)
					}
				}
				continue
			}

			for _, member := range msg.Channel.members {
				if msg.SenderID != member.ID { // Avoid sending the message back to the sender if any is provided
					member.SendMessage(formattedMsg)
				}
			}
		case <-s.shutdown:
			// Handle server shutdown
			if !s.stopped {
				for _, client := range s.clients {
					client.conn.Close()
				}
				s.stopped = true
			}

			if len(s.clients) == 0 {
				return // Exit if no clients are connected
			}
		}
	}
}

func (s *Server) Start() {
	s.wg.Add(1)
	go s.run()

	// Use hostname:port for net.Listen, not the URL string
	listenAddr := s.url.Hostname() + ":" + s.url.Port()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		s.logger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	s.logger.Info("Server is running", "address", s.url.Hostname(), "port", s.url.Port())

	// Start listening for incoming connections
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return // Listener closed, exit the loop
				}

				s.logger.Error("Failed to accept connection", "error", err)
				continue
			}

			s.register <- NewClient(conn, s, "", maxBucketSize, bucketRate) // Queue new client for registration
		}
	}()

	// Handle graceful shutdown on interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	// Initiate shutdown
	s.logger.Info("Shutting down server...")
	listener.Close()

	s.broadcastMessage(nil, nil, "Server is shutting down. Disconnecting...", true)

	close(s.shutdown) // Signal shutdown to all goroutines
	s.wg.Wait()       // Wait for all goroutines to finish
	s.logger.Info("Server has shut down.")
}

func (s *Server) broadcastMessage(client *Client, channel *Channel, msg string, isServerMsg bool) {
	senderName := ""
	senderID := ""
	if client != nil {
		senderName = client.GetUsername()
		senderID = client.ID
	}

	if isServerMsg {
		senderName = "Server"
	}

	s.broadcast <- Message{
		SenderName: senderName,
		SenderID:   senderID,
		Channel:    channel,
		Content:    msg,
	}
}
