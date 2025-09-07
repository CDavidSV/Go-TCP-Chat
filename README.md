# Go-TCP-Chat

Go-TCP-Chat is a TCP-based chat application implemented in Golang. It supports multiple clients, chat rooms, and various commands for an interactive chatting experience.

## Features
- **Chat Rooms**: Users can create and join chat rooms.
- **Commands**: Includes commands like `/join`, `/leave`, `/clients`, `/members`, `/channels`, `/name`, and `/help`.
- **User Management**: Users can change their usernames and view connected clients.

## Usage

### Running Locally
1. **Install Go**: Ensure you have Go installed on your system.
2. **Clone the Repository**:
   ```bash
   git clone https://github.com/CDavidSV/Go-TCP-Chat.git
   cd Go-TCP-Chat
   ```
3. **Build the Application**:
   ```bash
   go build -o server ./server
   go build -o client ./client
   ```
4. **Run the Server**:
   ```bash
   ./server
   ```
5. **Run the Client**:
   ```bash
   ./client
   ```

### Running with Docker
1. **Build the Docker Image**:
   ```bash
   docker build -t go-tcp-chat .
   ```
2. **Run the Docker Container**:
   ```bash
   docker run -p 3000:3000 go-tcp-chat
   ```
3. **Connect Clients**:
   Use the client application to connect to `localhost:3000`.

### Important Note for Docker

When running the server in Docker, ensure that the server binds to `0.0.0.0` instead of `localhost`. Update the `main.go` file in the `server` directory as follows:

```go
func main() {
    server := NewServer("0.0.0.0", "3000")
    server.Start()
}
```

This allows the server to accept connections from outside the container.

## Commands
- `/join <channel_name>`: Join or create a channel.
- `/leave`: Leave the current channel.
- `/clients`: List all connected clients.
- `/members`: List members in the current channel.
- `/channels`: List all available channels.
- `/name <new_username>`: Change your username.
- `/help`: Display available commands.
