package distrolock

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"testing"
)

const (
	GET    = iota
	PUT    = iota
	APPEND = iota
)

type protocol struct {
	user   int32
	action int32
	lockid int32
	status bool
	value  int
}

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

func sender(conn net.Conn) {
	words := "hello world!"
	conn.Write([]byte(words))
	fmt.Println("send over")
}

/*
func TestClient(t *testing.T) {
	server := "127.0.0.1:10240"
	tcpAddr, err := net.ResolveTCPAddr("tcp4", server)
	checkError(err, "")

	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	checkError(err, "")
	fmt.Println("connect success")
	sender(conn)
}
*/

func TestRPCCli(t *testing.T) {
	client, err := rpc.Dial("tcp", "localhost:10240")
	if err != nil {
		log.Fatal("dialing:", err)
	}
	// Synchronous call
	//for i := range
	args := protocol{0, 1, 2, true, 3}
	var res protocol
	err = client.Call("Dummy.HandleFunc", args, res)
	checkError(err, "")
	fmt.Printf("user %d\naction %d\nlockid %d\nstatus %t\nvalue %d\n", res.user, res.action, res.lockid, res.status, res.value)
}
