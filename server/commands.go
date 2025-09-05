package main

type CommandFunc func(name string, args []string, client *Client, server *Server)

type Command struct {
	Name   string
	Args   []string
	Client *Client
}

func (s *Server) registerCoommand(name string, handler CommandFunc) {

}

func (s *Server) loadCommands() {

}
