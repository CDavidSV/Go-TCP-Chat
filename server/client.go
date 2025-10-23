package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var rateLimitMessages []string = []string{
	"Please slow down your messages.",
	"You're sending messages too quickly.",
	"Take a moment before sending another message.",
	"Easy there! Let's keep the chat friendly.",
	"Whoa! Let's give others a chance to speak.",
	"Let's keep the conversation flowing smoothly.",
	"Patience is a virtue, especially in chat.",
	"Let's take a breather before the next message.",
	"Remember, good things come to those who wait.",
	"Let's keep the chat enjoyable for everyone.",
}

type Client struct {
	ID            string
	IP            string // Client's IP address (used as initial key)
	Username      atomic.Value
	registered    atomic.Bool
	conn          net.Conn
	channel       atomic.Value
	server        *Server
	send          chan string
	bucket        int
	maxBucketSize int
	bucketRate    float64 // tokens per second to refill
	lastRequest   time.Time
}

func NewClient(conn net.Conn, server *Server, name string, maxBucketSize int, bucketRate float64) *Client {
	id := uuid.NewString()
	// Extract IP address from connection
	ip := conn.RemoteAddr().String()

	client := &Client{
		ID:            id,
		IP:            ip,
		Username:      atomic.Value{},
		registered:    atomic.Bool{},
		channel:       atomic.Value{},
		conn:          conn,
		server:        server,
		maxBucketSize: maxBucketSize,
		send:          make(chan string, 1024), // Buffered channel to prevent blocking
		bucketRate:    bucketRate,
	}

	client.Username.Store(name)
	client.registered.Store(false) // Not registered until username is set

	return client
}

func (c *Client) Read() {
	defer func() {
		c.server.unregister <- c
		c.conn.Close()
	}()

	reader := bufio.NewReader(c.conn)
	for {
		c.conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		msg, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			if errors.Is(err, io.EOF) {
				// Client closed the connection
				return
			}

			c.server.logger.Error("Error reading from client", "client_id", c.ID, "error", err)
			return
		}

		// We get the elapsed time since the last request
		now := time.Now()
		elapsed := now.Sub(c.lastRequest).Seconds()

		// We used this to determine how many tokens we should add to the bucket
		tokens := elapsed * c.bucketRate
		c.bucket = int(math.Min(float64(c.bucket)+tokens, float64(c.maxBucketSize)))
		c.lastRequest = time.Now()

		if c.bucket <= 0 {
			randIndex := rand.IntN(len(rateLimitMessages))
			c.SendMessage(formatMessage("", "Server", fmt.Sprintf("You are being rate limited. %s", rateLimitMessages[randIndex])))
			continue
		}

		// Decrease the number of tokens in the bucket
		c.bucket--

		// Check if the message contains a pipe character
		// If it does, it's a malformed message
		if strings.Contains(msg, "|") {
			c.SendMessage(formatMessage("", "Server", "Malformed message. Please avoid using the '|' character."))
			continue
		}

		// This is always done after the user connects to the server
		// If the message contains spaces, only the first part is used as the username
		if !c.IsRegistered() {
			username := strings.TrimSpace(msg)
			if strings.Contains(username, " ") {
				username = strings.SplitN(username, " ", 2)[0]
			}

			// Request username change through server channel
			// Use IP as old key for first-time registration
			response := make(chan error, 1)
			c.server.setUsername <- UsernameChange{
				Client:      c,
				OldKey:      c.IP, // Use IP address as old key
				NewUsername: username,
				Response:    response,
			}

			// Wait for response
			if err := <-response; err != nil {
				c.SendMessage(formatMessage("", "Server", fmt.Sprintf("Failed to set username: %s", err.Error())))
				continue
			}

			c.SetRegistered(true)
			c.SendMessage(formatMessage("", "Server", fmt.Sprintf("Your username has been set to '%s'. Use /join <channel_name> to join a channel.", username)))
			continue
		}

		// Check if the message is a command (starts with '/')
		msg = strings.TrimSpace(msg)
		if after, ok := strings.CutPrefix(msg, "/"); ok {
			args := strings.Fields(after)
			if len(args) == 0 {
				c.SendMessage(formatMessage("", "Server", "No command provided."))
				continue // Continue listening for messages
			}

			c.server.command <- Command{
				Client: c,
				Args:   args[1:],
				Name:   args[0],
			}
			continue
		}

		// Regular message
		channel := c.GetChannel()
		if channel == nil {
			c.SendMessage(formatMessage("", "Server", "You are not in a channel. Use /join <channel> to join one."))
			continue
		}

		c.server.broadcastMessage(c, channel, msg, false)
	}
}

func (c *Client) Write() {
	defer func() {
		c.conn.Close()
	}()

	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

		header := make([]byte, 4)
		binary.LittleEndian.PutUint32(header, uint32(len(msg)))

		finalMessage := append(header, []byte(msg)...)
		if _, err := c.conn.Write(finalMessage); err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			c.server.logger.Error("Error writing to client", "client_id", c.ID, "error", err)
			return
		}
	}
}

func (c *Client) SendMessage(msg string) {
	select {
	case c.send <- msg:
	default:
		// If the send buffer is full, drop the message to avoid blocking
		c.server.logger.Warn("Send buffer full, dropping message", "client_id", c.ID)
	}
}

func (c *Client) SetUsername(newName string) {
	c.Username.Store(newName)
}

func (c *Client) GetUsername() string {
	return c.Username.Load().(string)
}

func (c *Client) GetChannel() *Channel {
	channel := c.channel.Load()
	if channel == nil {
		return nil
	}
	return channel.(*Channel)
}

func (c *Client) SetChannel(ch *Channel) {
	c.channel.Store(ch)
}

func (c *Client) IsRegistered() bool {
	return c.registered.Load()
}

func (c *Client) SetRegistered(registered bool) {
	c.registered.Store(registered)
}
