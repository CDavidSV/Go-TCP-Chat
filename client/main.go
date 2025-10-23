package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	gap           = "\n\n"
	host          = "localhost:3000"
	senderStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	serverStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	clients       = make(map[string]lipgloss.Style) // clientID -> style color
	slashCommands = []string{
		"/help",
		"/name",
		"/channels",
		"/join",
		"/leave",
		"/members",
		"/clients",
		"/whisper",
	}
)

type errMsg error
type Message struct {
	Content    string
	SenderID   string
	SenderName string
}

type model struct {
	viewport        viewport.Model
	messages        []string
	textarea        textarea.Model
	conn            net.Conn
	err             error
	commandsHistory []string
	historyIndex    int
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
	// vp.SetContent("Welcome back, type /help for commands")

	// Disable default key bindings for scrolling
	vp.KeyMap.Up.SetKeys(tea.KeyShiftUp.String())
	vp.KeyMap.Down.SetKeys(tea.KeyShiftDown.String())
	vp.KeyMap.PageUp.SetKeys(tea.KeyCtrlShiftUp.String())
	vp.KeyMap.PageDown.SetKeys(tea.KeyCtrlShiftDown.String())

	ta.KeyMap.InsertNewline.SetEnabled(false)

	return model{
		viewport:        vp,
		textarea:        ta,
		messages:        make([]string, 0),
		conn:            c,
		commandsHistory: make([]string, 0),
		historyIndex:    0,
		err:             nil,
	}
}

func getForegroundColor() lipgloss.Color {
	// Gets a color based on the number of connected clients
	colorCode := 1 + len(clients)%255

	// Skips colors that are used for server and sender styles
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

	m.err = nil
	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.textarea.SetWidth(msg.Width)
		m.viewport.Height = msg.Height - m.textarea.Height() - lipgloss.Height(gap)

		// Rerender messages to fit new width
		if len(m.messages) > 0 {
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.messages, "\n")))
		}

		m.viewport.GotoBottom()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			inputValue := m.textarea.Value()

			if strings.TrimSpace(inputValue) == "" {
				m.textarea.Reset()
				return m, nil
			}

			if strings.Contains(inputValue, "|") {
				m.err = errors.New("the '|' character is not allowed")
				return m, nil
			}

			// Check if it is a command
			if strings.HasPrefix(inputValue, "/") {
				if slices.Contains(slashCommands, strings.Split(inputValue, " ")[0]) {
					// Valid command, add to history
					m.commandsHistory = append(m.commandsHistory, inputValue)
					m.historyIndex = len(m.commandsHistory)
				}
			}

			_, err := m.conn.Write([]byte(inputValue + "\n"))
			if err != nil {
				// Error sending message to the serve
				m.err = err
				return m, nil
			}

			m.messages = append(m.messages, senderStyle.Render("You: ")+m.textarea.Value())
			m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.messages, "\n")))
			m.textarea.Reset()
			m.viewport.GotoBottom()
		case tea.KeyTab:
			inputValue := m.textarea.Value()

			// Only autocomplete if it starts with a slash (for commands)
			if !strings.HasPrefix(inputValue, "/") {
				return m, nil
			}

			// Check if the input matches any of the commands and autocomplete
			for _, cmd := range slashCommands {
				if strings.HasPrefix(cmd, inputValue) {
					m.textarea.SetValue(cmd)
					m.textarea.SetCursor(len(cmd))
					break
				}
			}
		case tea.KeyUp:
			if len(m.commandsHistory) == 0 {
				return m, nil
			}

			// Move up in history, but not out of bounds
			if m.historyIndex > 0 {
				m.historyIndex--
			}

			command := m.commandsHistory[m.historyIndex]
			m.textarea.SetValue(command)
			m.textarea.SetCursor(len(command))
		case tea.KeyDown:
			if len(m.commandsHistory) == 0 || m.historyIndex >= len(m.commandsHistory)-1 {
				return m, nil
			}

			// Move down in history, but not out of bounds
			m.historyIndex++
			m.textarea.SetValue(m.commandsHistory[m.historyIndex])
			m.textarea.SetCursor(len(m.commandsHistory[m.historyIndex]))
		case tea.KeyBackspace:
			if m.textarea.Value() == "" {
				m.historyIndex = len(m.commandsHistory)
			}
		default:
			// Ignore all unhandled keys to prevent unintended behavior
			return m, nil
		}
	case Message:
		// If the sender name is "Server", use the server style
		// If the sender ID is "." or empty, it's a broadcast message from the server
		// Otherwise, use or create a style for the client
		if msg.SenderName == "Server" {
			m.messages = append(m.messages, serverStyle.Render("[Server]: ")+msg.Content)
		} else if msg.SenderID == "." || msg.SenderID == "" {
			m.messages = append(m.messages, msg.Content)
		}

		if msg.SenderID != "." {
			newStyle, ok := clients[msg.SenderID]
			if !ok {
				// If the sender ID is not in the clients map, create a new style for it
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
	errMsg := ""
	if m.err != nil {
		errMsg = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("Error: %v", m.err)) + "\n"
	}

	return fmt.Sprintf(
		"%s%s%s%s",
		m.viewport.View(),
		gap,
		errMsg,
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
	defer func() {
		p.Quit()
	}()

	for {
		// Read the header first (4 bytes)
		header := make([]byte, 4)
		_, err := conn.Read(header)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return // Connection closed
			}

			if errors.Is(err, io.EOF) {
				// The server closed the connection
				p.Send(errMsg(fmt.Errorf("disconnected from server")))
				return
			}

			p.Send(errMsg(fmt.Errorf("error reading header from server: %w", err)))
			return
		}

		// Get the message size from the header, which lets us know how many bytes to read next
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
