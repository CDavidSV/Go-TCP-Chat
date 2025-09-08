package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
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
	Username      string
	conn          net.Conn
	channel       *Channel
	server        *Server
	send          chan string
	mu            sync.RWMutex
	bucket        int
	maxBucketSize int
	bucketRate    float64 // tokens per second to refill
	lastRequest   time.Time
}

func NewClient(conn net.Conn, server *Server, name string, maxBucketSize int, bucketRate float64) *Client {
	return &Client{
		ID:            uuid.NewString(),
		Username:      name,
		conn:          conn,
		server:        server,
		maxBucketSize: maxBucketSize,
		send:          make(chan string, 512), // Buffered channel to prevent blocking
		bucketRate:    bucketRate,
	}
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

			if err.Error() != "EOF" {
				c.server.logger.Error("Error reading from client", "client_id", c.ID, "error", err)
				return
			}
			continue
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

		msg = strings.TrimSpace(msg)
		if after, ok := strings.CutPrefix(msg, "/"); ok {
			args := strings.Fields(strings.ToLower(after))
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
		// msg += "\n"
		binary.LittleEndian.PutUint32(header, uint32(len(msg)))
		_, err := c.conn.Write(header)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			c.server.logger.Error("Error writing header to client", "client_id", c.ID, "error", err)
			return
		}

		_, err = c.conn.Write([]byte(msg))
		if err != nil {
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
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Username = newName
}

func (c *Client) GetUsername() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Username
}

func (c *Client) GetChannel() *Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.channel
}

func (c *Client) SetChannel(ch *Channel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channel = ch
}
