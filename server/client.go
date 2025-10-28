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
)

var rateLimitMessages []string = []string{
	"Please slow down your messages.",
	"You're sending messages too quickly.",
	"Take a moment before sending another message.",
	"Easy there! Let's keep the chat friendly.",
	"Whoa! Let's give others a chance to speak.",
	"Let's keep the conversation flowing smoothly.",
	"Let's take a breather before the next message.",
	"Let's keep the chat enjoyable for everyone.",
}

type Client struct {
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
	reader        *bufio.Reader
	writer        *bufio.Writer
}

func NewClient(conn net.Conn, server *Server, name string, maxBucketSize int, bucketRate float64) *Client {
	// Extract IP address from connection
	ip := conn.RemoteAddr().String()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	client := &Client{
		IP:            ip,
		Username:      atomic.Value{},
		registered:    atomic.Bool{},
		channel:       atomic.Value{},
		conn:          conn,
		server:        server,
		maxBucketSize: maxBucketSize,
		send:          make(chan string, 1024),
		bucketRate:    bucketRate,
		reader:        reader,
		writer:        writer,
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

	for {
		c.conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
		msg, err := c.reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Client closed the connection
				return
			}

			var opErr *net.OpError
			if errors.As(err, &opErr) {
				// Connection was closed or reset by peer
				return
			}

			// Check for timeout
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				c.server.logger.Info("Client read timeout", "username", c.GetUsername())
				return
			}

			c.server.logger.Error("Error reading from client", "error", err)
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
			c.SendMessage(formatMessage("Server", fmt.Sprintf("You are being rate limited. %s", rateLimitMessages[randIndex])))
			continue
		}

		// Decrease the number of tokens in the bucket
		c.bucket--

		// Check if the message contains a pipe character
		// If it does, it's a malformed message
		if strings.Contains(msg, "|") {
			c.SendMessage(formatMessage("Server", "Malformed message. Please avoid using the '|' character."))
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
				c.SendMessage(formatMessage("Server", fmt.Sprintf("Failed to set username: %s", err.Error())))
				continue
			}

			c.SetRegistered(true)
			c.SendMessage(formatMessage("Server", fmt.Sprintf("Your username has been set to '%s'. Use /join <channel_name> to join a channel.", username)))
			continue
		}

		// Check if the message is a command (starts with '/')
		msg = strings.TrimSpace(msg)
		if after, ok := strings.CutPrefix(msg, "/"); ok {
			args := strings.Fields(after)
			if len(args) == 0 {
				c.SendMessage(formatMessage("Server", "No command provided."))
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
			c.SendMessage(formatMessage("Server", "You are not in a channel. Use /join <channel> to join one."))
			continue
		}

		c.server.broadcastMessage(c, channel, msg)
	}
}

func (c *Client) Write() {
	defer func() {
		c.writer.Flush()
		c.conn.Close()
	}()

	for msg := range c.send {
		c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

		header := make([]byte, 4)
		binary.LittleEndian.PutUint32(header, uint32(len(msg)))

		if _, err := c.writer.Write(header); err != nil {
			c.handleWriteError(err, "header write")
			return
		}

		if _, err := c.writer.WriteString(msg); err != nil {
			c.handleWriteError(err, "body write")
			return
		}

		if err := c.writer.Flush(); err != nil {
			c.handleWriteError(err, "flush")
			return
		}
	}
}

func (c *Client) handleWriteError(err error, context string) {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return
	}

	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		c.server.logger.Info(fmt.Sprintf("Client write timeout (%s)", context), "username", c.GetUsername())
		return
	}

	c.server.logger.Error(fmt.Sprintf("Error writing to client (%s)", context), "error", err)
}

func (c *Client) SendMessage(msg string) {
	select {
	case c.send <- msg:
	default:
		// If the send buffer is full, drop the message to avoid blocking
		c.server.logger.Warn("Send buffer full, dropping message", "username", c.GetUsername())

		// Close the client connection. This can occur if the client is too slow to read messages or is spamming too many messages causing buffer overflow.
		c.conn.Close()
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
