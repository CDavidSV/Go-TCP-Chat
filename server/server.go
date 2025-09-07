package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type Server struct {
	clients    map[string]*Client
	channels   map[string]*Channel
	commands   map[string]CommandFunc
	command    chan Command
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
	shutdown   chan struct{}
	url        *url.URL
	logger     *slog.Logger
	wg         sync.WaitGroup
	stopped    bool
}

func NewServer(address, port string) *Server {
	url, err := url.Parse("tcp://" + address + ":" + port)
	if err != nil {
		panic("Failed to parse server URL: " + err.Error())
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	server := &Server{
		clients:    make(map[string]*Client),
		channels:   make(map[string]*Channel),
		commands:   make(map[string]CommandFunc),
		command:    make(chan Command),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Message, 1024), // Buffered channel to prevent blocking
		shutdown:   make(chan struct{}),
		url:        url,
		logger:     logger,
		stopped:    false,
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

	message := fmt.Sprintf("%s|%s|%s", senderID, senderName, content)
	return message
}

func (s *Server) run() {
	defer s.wg.Done()

	// TODO: Implement main event loop
	for {
		select {
		case client := <-s.register:
			// Handle new client registration
			s.clients[client.ID] = client
			s.logger.Info("Client connected", "client_id", client.ID, "ip", client.conn.RemoteAddr().String(), "total_clients", len(s.clients))

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
				s.broadcastMessage(client, clientChannel, fmt.Sprintf("%s has left the channel.", client.Username), true)
				if len(clientChannel.members) == 0 {
					delete(s.channels, clientChannel.Name)
				}
			}

			delete(s.clients, client.ID)
			close(client.send)
			s.logger.Info("Client disconnected", "client_id", client.ID, "ip", client.conn.RemoteAddr().String(), "total_clients", len(s.clients))
		case cmd := <-s.command:
			// Handle commands from clients
			if cmdFunc, exists := s.commands[cmd.Name]; exists {
				cmdFunc(cmd.Name, cmd.Args, cmd.Client, s) // Execute command if found
			} else {
				cmd.Client.SendMessage(formatMessage("", "Server", "[Server]: Unknown command. Type /help for a list of commands."))
			}
		case msg := <-s.broadcast:
			clientSender := msg.SenderID
			if msg.IsServerMsg {
				msg.SenderName = "Server"
				clientSender = ""
			}

			// Handle broadcasting messages to clients
			if msg.Channel == nil {
				// Broadcast message to all clients if no channel is specified
				for _, client := range s.clients {
					if msg.SenderID != client.ID { // Avoid sending the message back to the sender if any is provided
						client.SendMessage(formatMessage(clientSender, msg.SenderName, msg.Content))
					}
				}
				continue
			}

			for _, member := range msg.Channel.members {
				if msg.SenderID != member.ID { // Avoid sending the message back to the sender if any is provided
					member.SendMessage(formatMessage(clientSender, msg.SenderName, msg.Content))
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

			s.register <- NewClient(conn, s, "Anonymous") // Queue new client for registration
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
	s.broadcast <- Message{
		SenderName:  client.Username,
		SenderID:    client.ID,
		Channel:     channel,
		Content:     msg,
		IsServerMsg: isServerMsg,
	}
}
