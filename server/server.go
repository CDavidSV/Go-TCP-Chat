package main

import (
	"errors"
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

func (s *Server) run() {
	s.wg.Add(1)
	defer s.wg.Done()

	// TODO: Implement main event loop
	for {
		select {
		case client := <-s.register:
			// Handle new client registration
		case client := <-s.unregister:
			// Handle client unregistration
		case msg := <-s.broadcast:
			// Handle broadcasting messages to clients
		case cmd := <-s.command:
			// Handle commands from clients
		case <-s.shutdown:
			// Handle server shutdown
			return
		}
	}
}

func (s *Server) Start() {
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

	s.broadcastMessage(Message{
		Content: "Server is shutting down. Disconnecting...",
	})

	close(s.shutdown) // Signal shutdown to all goroutines
	s.wg.Wait()       // Wait for all goroutines to finish
	s.logger.Info("Server has shut down.")
}

func (s *Server) broadcastMessage(msg Message) {

}
