package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	gap         = "\n\n"
	host        = "localhost:3000"
	senderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	serverStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	clients     = make(map[string]lipgloss.Style) // clientID -> style color
)

type errMsg error
type Message struct {
	Content    string
	SenderID   string
	SenderName string
}

type model struct {
	viewport viewport.Model
	messages []string
	textarea textarea.Model
	conn     net.Conn
	err      error
}

func initialModel(c net.Conn) model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."

	ta.Focus()

	ta.Prompt = "| "
	ta.CharLimit = 280

	ta.SetWidth(30)
	ta.SetHeight(3)

	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false

	vp := viewport.New(30, 10)
	vp.SetContent("Welcome back, type /help for commands")

	ta.KeyMap.InsertNewline.SetEnabled(false)

	return model{
		viewport: vp,
		textarea: ta,
		messages: make([]string, 0),
		conn:     c,
		err:      nil,
	}
}

func getForegroundColor() lipgloss.Color {
	colorCode := 1 + len(clients)%255

	if colorCode == 5 || colorCode == 2 {
		colorCode++
	}

	return lipgloss.Color(strconv.Itoa(colorCode))
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.textarea.SetWidth(msg.Width)
		m.viewport.Height = msg.Height - m.textarea.Height() - lipgloss.Height(gap)

		if len(m.messages) > 0 {
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.messages, "\n")))
		}

		m.viewport.GotoBottom()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			_, err := m.conn.Write([]byte(m.textarea.Value() + "\n"))
			if err != nil {
				// Error sending message to the serve
				m.err = err
				return m, nil
			}

			m.messages = append(m.messages, senderStyle.Render("You: ")+m.textarea.Value())
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.messages, "\n")))
			m.textarea.Reset()
			m.viewport.GotoBottom()
		}
	case Message:
		if msg.SenderName == "Server" {
			m.messages = append(m.messages, serverStyle.Render("[Server]: ")+msg.Content)
		} else if msg.SenderID == "." || msg.SenderID == "" {
			m.messages = append(m.messages, msg.Content)
		}

		if msg.SenderID != "." {
			newStyle, ok := clients[msg.SenderID]
			if !ok {
				// Generate a random color for the new client
				newStyle = lipgloss.NewStyle().Foreground(getForegroundColor())
				clients[msg.SenderID] = newStyle
			}
			m.messages = append(m.messages, newStyle.Render("["+msg.SenderName+"]: ")+msg.Content)
		}

		m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.messages, "\n")))
		m.viewport.GotoBottom()
	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m model) View() string {
	return fmt.Sprintf(
		"%s%s%s",
		m.viewport.View(),
		gap,
		m.textarea.View(),
	)
}

func connectToServer() (net.Conn, error) {
	conn, err := net.Dial("tcp", host)
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		return nil, err
	}

	return conn, nil
}

func listener(conn net.Conn, p *tea.Program) {
	for {
		header := make([]byte, 4)
		_, err := conn.Read(header)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			p.Send(errMsg(fmt.Errorf("error reading header from server: %w", err)))
			return
		}

		// Get the message size from the header
		msgSize := binary.LittleEndian.Uint32(header)

		// Create a buffer to hold the incoming message
		body := make([]byte, msgSize)
		_, err = conn.Read(body)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			p.Send(errMsg(fmt.Errorf("error reading from server: %w", err)))
			return
		}

		// Use lipgloss to style incoming messages
		message := string(body)
		parts := strings.SplitN(message, "|", 3) // Expects three parts: senderID, senderName, content

		if len(parts) != 3 {
			fmt.Println("Invalid message format, skipping:", message)
			continue // Skip processing this message
		}

		senderID := parts[0]
		senderName := parts[1]
		content := parts[2]

		p.Send(Message{
			Content:    content,
			SenderID:   senderID,
			SenderName: senderName,
		})
	}
}

func main() {
	conn, err := connectToServer()
	if err != nil {
		log.Fatal("Failed to connect to server:", err)
	}
	defer conn.Close() // Close the connection once the program ends

	p := tea.NewProgram(initialModel(conn))

	go listener(conn, p)

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
