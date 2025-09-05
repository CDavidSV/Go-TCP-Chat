package main

func main() {
	server := NewServer("localhost", "3000")
	server.Start()
}
