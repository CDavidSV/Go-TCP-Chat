package main

type Channel struct {
	Name    string
	members map[string]*Client
}

type Message struct {
	SenderID   string
	Channel    *Channel
	SenderName string
	Content    string
}

func NewChannel(name string) *Channel {
	return &Channel{
		Name:    name,
		members: make(map[string]*Client),
	}
}

func (ch *Channel) AddMember(client *Client) {
	ch.members[client.ID] = client
}

func (ch *Channel) RemoveMember(client *Client) {
	delete(ch.members, client.ID)
}
