package main

import "flag"

func main() {
	host := flag.String("host", "localhost", "The host to listen on")
	port := flag.String("port", "3000", "The port to listen on")
	flag.Parse()

	server := NewServer(*host, *port)
	server.Start()
}
