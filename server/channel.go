package main

import "errors"

var (
	ErrIncorrectPassword = errors.New("incorrect password")
)

type Channel struct {
	Name     string
	members  map[string]*Client
	password string
}

type Message struct {
	SenderID   string
	Channel    *Channel
	SenderName string
	Content    string
}

func NewChannel(name, password string) *Channel {
	return &Channel{
		Name:     name,
		members:  make(map[string]*Client),
		password: password,
	}
}

func (ch *Channel) AddMember(client *Client, password string) error {
	// If the channel has a password, check it
	if ch.password != "" && ch.password != password {
		return ErrIncorrectPassword
	}

	ch.members[client.ID] = client
	return nil
}

func (ch *Channel) RemoveMember(client *Client) {
	delete(ch.members, client.ID)
}

func (ch *Channel) RequiresPassword() bool {
	return ch.password != ""
}

func (ch *Channel) ValidatePassword(password string) bool {
	return ch.password == password
}
