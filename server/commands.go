package main

import (
	"fmt"
	"strings"
)

type CommandFunc func(name string, args []string, client *Client, server *Server)

type Command struct {
	Name   string
	Args   []string
	Client *Client
}

func joinChannel(name string, args []string, client *Client, server *Server) {
	if len(args) < 1 {
		client.SendMessage(formatMessage("", "Server", "Usage: /join <channel_name> [password]"))
		return
	}

	maxPasswordLength := 32
	password := ""
	if len(args) > 1 {
		password = args[1]
	}

	if len(password) > maxPasswordLength {
		client.SendMessage(formatMessage("", "Server", fmt.Sprintf("Password is too long. Maximum length is %d characters.", maxPasswordLength)))
		return
	}

	channelName := args[0]
	channel, exists := server.channels[channelName]
	if !exists {
		channel = NewChannel(channelName, password)

		server.channels[channelName] = channel
	}

	joinedChannel := client.GetChannel()

	if joinedChannel != nil {
		joinedChannel.RemoveMember(client)

		server.broadcastMessage(client, joinedChannel, fmt.Sprintf("%s has left the channel.", client.GetUsername()), true)
		if len(joinedChannel.members) == 0 {
			delete(server.channels, joinedChannel.Name)
		}
	}

	if channel.RequiresPassword() && password == "" {
		client.SendMessage(formatMessage("", "Server", fmt.Sprintf("Channel '%s' requires a password.", channelName)))
		return
	}

	if err := channel.AddMember(client, password); err != nil {
		client.SendMessage(formatMessage("", "Server", fmt.Sprintf("Incorrect password for channel '%s'", channelName)))
		return
	}

	client.SetChannel(channel)
	server.broadcastMessage(client, channel, fmt.Sprintf("%s has joined the channel.", client.GetUsername()), true)
	client.SendMessage(formatMessage("", "Server", fmt.Sprintf("You have joined channel '%s'", channel.Name)))
}

func leaveChannel(name string, args []string, client *Client, server *Server) {
	joinedChannel := client.GetChannel()

	if joinedChannel == nil {
		client.SendMessage(formatMessage("", "Server", "You are not in any channel."))
		return
	}
	joinedChannel.RemoveMember(client)
	server.broadcastMessage(client, joinedChannel, fmt.Sprintf("%s has left the channel.", client.GetUsername()), true)

	if len(joinedChannel.members) == 0 {
		delete(server.channels, joinedChannel.Name)
	}

	client.SetChannel(nil)
	client.SendMessage(formatMessage("", "Server", fmt.Sprintf("You have left channel '%s'", joinedChannel.Name)))
}

func connectedClients(name string, args []string, client *Client, server *Server) {
	client.SendMessage(formatMessage("", "Server", fmt.Sprintf("Connected clients (%d)", len(server.clients))))
}

func channelMembers(name string, args []string, client *Client, server *Server) {
	joinedChannel := client.GetChannel()

	if joinedChannel == nil {
		client.SendMessage(formatMessage("", "", "You are not in any channel."))
		return
	}

	var members []string
	for _, member := range joinedChannel.members {
		members = append(members, member.GetUsername())
	}
	client.SendMessage(formatMessage("", "", fmt.Sprintf("Members in channel '%s': \n%s", joinedChannel.Name, strings.Join(members, ", "))))
}

func listChannels(name string, args []string, client *Client, server *Server) {
	if len(server.channels) == 0 {
		client.SendMessage(formatMessage("", "", "No channels available."))
		return
	}

	var channelNames []string
	for channelName, channel := range server.channels {
		channelNames = append(channelNames, channelName+fmt.Sprintf(" (%d)", len(channel.members)))
	}
	client.SendMessage(formatMessage("", "", fmt.Sprintf("Available channels: \n%s", strings.Join(channelNames, ","))))
}

func changeName(name string, args []string, client *Client, server *Server) {
	if len(args) < 1 {
		client.SendMessage(formatMessage("", "Server", "Usage: /name <new_username>"))
		return
	}

	newName := args[0]
	client.SetUsername(newName)
	client.SendMessage(formatMessage("", "Server", fmt.Sprintf("Your username has been changed to '%s'", newName)))
}

func help(name string, args []string, client *Client, server *Server) {
	helpText := `Available commands:
/join <channel_name> [password] - Join or create a channel
/leave - Leave the current channel
/clients - Get the number of connected clients
/members - List members in your current channel
/channels - List all available channels
/name <new_username> - Change your username
/help - Show this help message

Note: Arguments in <> are required, arguments in [] are optional.
`

	client.SendMessage(formatMessage("", "", helpText))
}

func (s *Server) loadCommands() {
	s.commands["join"] = joinChannel
	s.commands["leave"] = leaveChannel
	s.commands["clients"] = connectedClients
	s.commands["members"] = channelMembers
	s.commands["channels"] = listChannels
	s.commands["name"] = changeName
	s.commands["help"] = help
}
