package main

import (
	"log"
	"net"
	"net/rpc"
	"os"
)

const (
	GET    = iota
	PUT    = iota
	APPEND = iota
)

type Protocol struct {
	user   int32
	action int32
	lockid int32
	status bool
	value  int
}

type Dummy int

func checkError(err error, info string) {
	if err != nil {
		log.Printf("Fatal error:%s\n", err.Error())
		os.Exit(1)
	}
}

func checkErrorBool(err error) bool {
	if err != nil {
		log.Println("Fatal error: " + err.Error())
		return false
	}
	return true
}

func (t *Dummy) HandleFunc(req *Protocol, resp *Protocol) error {
	resp = req
	resp.lockid += 100
	return nil
}

func main() {
	tcpAddr, err := net.ResolveTCPAddr("tcp", ":10240")
	checkError(err, "")
	listener, err := net.ListenTCP("tcp", tcpAddr)
	checkError(err, "")
	defer listener.Close()
	for {
		conn, err := listener.Accept()

		if err != nil {
			continue
		}
		rpc.ServeConn(conn)
	}

}
