package raft

import (
	"labrpc"
	"log"
	"math/rand"
	"runtime"
	"sync"
	"testing"

	crand "crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"
)

func randstring(n int) string {
	b := make([]byte, 2*n)
	crand.Read(b)
	s := base64.URLEncoding.EncodeToString(b)
	return s[0:n]
}

func makeSeed() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := crand.Int(crand.Reader, max)
	x := bigx.Int64()
	return x
}

type config struct {
	mu        sync.Mutex
	t         *testing.T
	net       *labrpc.Network
	n         int
	rafts     []*Raft
	applyErr  []string // from apply channel readers
	connected []bool   // whether each server is on the net
	saved     []*Persister
	endnames  [][]string    // the port file names each sends to
	logs      []map[int]int // copy of each server's committed entries
	start     time.Time
	t0        time.Time // time at which test_test.go called cfg.begin()
	rpcs0     int       // rpcTotal() at start of test
	cmds0     int       // number of agreements
	maxIndex  int
	maxIndex0 int
}

var ncpu_once sync.Once

func make_config(t *testing.T, n int, unreliable bool) *config {
	ncpu_once.Do(func() {
		if runtime.NumCPU() < 2 {
			fmt.Printf("warning: only one CPU, which may conceal locking bugs\n")
		}
		rand.Seed(makeSeed())
	})
	runtime.GOMAXPROCS(4)
	cfg := &config{}
	cfg.t = t
	cfg.net = labrpc.MakeNetwork()
	cfg.n = n
	cfg.applyErr = make([]string, cfg.n)
	cfg.rafts = make([]*Raft, cfg.n)
	cfg.connected = make([]bool, cfg.n)
	cfg.saved = make([]*Persister, cfg.n)
	cfg.endnames = make([][]string, cfg.n)
	cfg.logs = make([]map[int]int, cfg.n)
	cfg.start = time.Now()

	cfg.setunreliable(unreliable)

	cfg.net.LongDelays(true)

	for i := 0; i < cfg.n; i++ {
		cfg.logs[i] = map[int]int{}
		cfg.start1(i)
	}

	for i := 0; i < cfg.n; i++ {
		cfg.connect(i)
	}

	return cfg
}

// shut down a Raft server but save its persistent state.
func (cfg *config) crash1(i int) {
	cfg.disconnect(i)
	cfg.net.DeleteServer(i)

	cfg.mu.Lock()
	defer cfg.mu.Unlock()

	if cfg.saved[i] != nil {
		cfg.saved[i] = cfg.saved[i].Copy()
	}

	rf := cfg.rafts[i]
	if rf != nil {
		cfg.mu.Unlock()
		rf.Kill()
		cfg.mu.Lock()
		cfg.rafts[i] = nil
	}

	if cfg.saved[i] != nil {
		raftlog := cfg.saved[i].ReadRaftState()
		cfg.saved[i] = &Persister{}
		cfg.saved[i].SaveRaftState(raftlog)
	}
}

//
func (cfg *config) start1(i int) {
	cfg.crash1(i)

	cfg.endnames[i] = make([]string, cfg.n)
	for j := 0; j < cfg.n; j++ {
		cfg.endnames[i][j] = randstring(20)
	}

	// a fresh set of ClientEnds.
	ends := make([]*labrpc.ClientEnd, cfg.n)
	for j := 0; j < cfg.n; j++ {
		ends[j] = cfg.net.MakeEnd(cfg.endnames[i][j])
		cfg.net.Connect(cfg.endnames[i][j], j)
	}

	cfg.mu.Lock()

	if cfg.saved[i] != nil {
		cfg.saved[i] = cfg.saved[i].Copy()
	} else {
		cfg.saved[i] = MakePersister()
	}

	cfg.mu.Unlock()

	applyCh := make(chan ApplyMsg)
	go func() {
		for m := range applyCh {
			err_msg := ""
			if m.CommandValid == false {
			} else if v, ok := (m.Command).(int); ok {
				cfg.mu.Lock()
				for j := 0; j < len(cfg.logs); j++ {
					if old, oldok := cfg.logs[j][m.CommandIndex]; oldok && old != v {
						err_msg = fmt.Sprintf("commit index=%v server=%v %v != server=%v %v",
							m.CommandIndex, i, m.Command, j, old)
					}
				}
				_, prevok := cfg.logs[i][m.CommandIndex-1]
				cfg.logs[i][m.CommandIndex] = v
				if m.CommandIndex > cfg.maxIndex {
					cfg.maxIndex = m.CommandIndex
				}
				cfg.mu.Unlock()

				if m.CommandIndex > 1 && prevok == false {
					err_msg = fmt.Sprintf("server %v apply out of order %v", i, m.CommandIndex)
				}
			} else {
				err_msg = fmt.Sprintf("committed command %v is not an int", m.Command)
			}

			if err_msg != "" {
				log.Fatalf("apply error: %v\n", err_msg)
				cfg.applyErr[i] = err_msg
			}
		}
	}()

	rf := Make(ends, i, cfg.saved[i], applyCh)

	cfg.mu.Lock()
	cfg.rafts[i] = rf
	cfg.mu.Unlock()

	svc := labrpc.MakeService(rf)
	srv := labrpc.MakeServer()
	srv.AddService(svc)
	cfg.net.AddServer(i, srv)
}

func (cfg *config) checkTimeout() {
	if !cfg.t.Failed() && time.Since(cfg.start) > 120*time.Second {
		cfg.t.Fatal("test took longer than 120 seconds")
	}
}

func (cfg *config) cleanup() {
	for i := 0; i < len(cfg.rafts); i++ {
		if cfg.rafts[i] != nil {
			cfg.rafts[i].Kill()
		}
	}
	cfg.net.Cleanup()
	cfg.checkTimeout()
}

func (cfg *config) connect(i int) {

	cfg.connected[i] = true

	for j := 0; j < cfg.n; j++ {
		if cfg.connected[j] {
			endname := cfg.endnames[i][j]
			cfg.net.Enable(endname, true)
		}
	}

	for j := 0; j < cfg.n; j++ {
		if cfg.connected[j] {
			endname := cfg.endnames[j][i]
			cfg.net.Enable(endname, true)
		}
	}
}

// detach server i from the net.
func (cfg *config) disconnect(i int) {
	// fmt.Printf("disconnect(%d)\n", i)

	cfg.connected[i] = false

	for j := 0; j < cfg.n; j++ {
		if cfg.endnames[i] != nil {
			endname := cfg.endnames[i][j]
			cfg.net.Enable(endname, false)
		}
	}

	for j := 0; j < cfg.n; j++ {
		if cfg.endnames[j] != nil {
			endname := cfg.endnames[j][i]
			cfg.net.Enable(endname, false)
		}
	}
}

func (cfg *config) rpcCount(server int) int {
	return cfg.net.GetCount(server)
}

func (cfg *config) rpcTotal() int {
	return cfg.net.GetTotalCount()
}

func (cfg *config) setunreliable(unrel bool) {
	cfg.net.Reliable(!unrel)
}

func (cfg *config) setlongreordering(longrel bool) {
	cfg.net.LongReordering(longrel)
}

// check that there's exactly one leader.
func (cfg *config) checkOneLeader() int {
	for iters := 0; iters < 10; iters++ {
		ms := 450 + (rand.Int63() % 100)
		time.Sleep(time.Duration(ms) * time.Millisecond)

		leaders := make(map[int][]int)
		for i := 0; i < cfg.n; i++ {
			if cfg.connected[i] {
				if term, leader := cfg.rafts[i].GetState(); leader {
					leaders[term] = append(leaders[term], i)
				}
			}
		}

		lastTermWithLeader := -1
		for term, leaders := range leaders {
			if len(leaders) > 1 {
				cfg.t.Fatalf("term %d has %d (>1) leaders", term, len(leaders))
			}
			if term > lastTermWithLeader {
				lastTermWithLeader = term
			}
		}

		if len(leaders) != 0 {
			return leaders[lastTermWithLeader][0]
		}
	}
	cfg.t.Fatalf("expected one leader, got none")
	return -1
}

// check that everyone agrees on the term.
func (cfg *config) checkTerms() int {
	term := -1
	for i := 0; i < cfg.n; i++ {
		if cfg.connected[i] {
			xterm, _ := cfg.rafts[i].GetState()
			if term == -1 {
				term = xterm
			} else if term != xterm {
				cfg.t.Fatalf("servers disagree on term")
			}
		}
	}
	return term
}

// check that there's no leader
func (cfg *config) checkNoLeader() {
	for i := 0; i < cfg.n; i++ {
		if cfg.connected[i] {
			_, is_leader := cfg.rafts[i].GetState()
			if is_leader {
				cfg.t.Fatalf("expected no leader, but %v claims to be leader", i)
			}
		}
	}
}

// how many servers think a log entry is committed?
func (cfg *config) nCommitted(index int) (int, interface{}) {
	count := 0
	cmd := -1
	for i := 0; i < len(cfg.rafts); i++ {
		if cfg.applyErr[i] != "" {
			cfg.t.Fatal(cfg.applyErr[i])
		}

		cfg.mu.Lock()
		cmd1, ok := cfg.logs[i][index]
		cfg.mu.Unlock()

		if ok {
			if count > 0 && cmd != cmd1 {
				cfg.t.Fatalf("committed values do not match: index %v, %v, %v\n",
					index, cmd, cmd1)
			}
			count += 1
			cmd = cmd1
		}
	}
	return count, cmd
}

// wait for at least n servers to commit.
func (cfg *config) wait(index int, n int, startTerm int) interface{} {
	to := 10 * time.Millisecond
	for iters := 0; iters < 30; iters++ {
		nd, _ := cfg.nCommitted(index)
		if nd >= n {
			break
		}
		time.Sleep(to)
		if to < time.Second {
			to *= 2
		}
		if startTerm > -1 {
			for _, r := range cfg.rafts {
				if t, _ := r.GetState(); t > startTerm {
					return -1
				}
			}
		}
	}
	nd, cmd := cfg.nCommitted(index)
	if nd < n {
		cfg.t.Fatalf("only %d decided for index %d; wanted %d\n",
			nd, index, n)
	}
	return cmd
}

func (cfg *config) one(cmd int, expectedServers int, retry bool) int {
	t0 := time.Now()
	starts := 0
	for time.Since(t0).Seconds() < 10 {
		index := -1
		for si := 0; si < cfg.n; si++ {
			starts = (starts + 1) % cfg.n
			var rf *Raft
			cfg.mu.Lock()
			if cfg.connected[starts] {
				rf = cfg.rafts[starts]
			}
			cfg.mu.Unlock()
			if rf != nil {
				index1, _, ok := rf.Start(cmd)
				if ok {
					index = index1
					break
				}
			}
		}

		if index != -1 {
			t1 := time.Now()
			for time.Since(t1).Seconds() < 2 {
				nd, cmd1 := cfg.nCommitted(index)
				if nd > 0 && nd >= expectedServers {
					if cmd2, ok := cmd1.(int); ok && cmd2 == cmd {
						return index
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if retry == false {
				cfg.t.Fatalf("one(%v) failed to reach agreement", cmd)
			}
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}
	cfg.t.Fatalf("one(%v) failed to reach agreement", cmd)
	return -1
}

func (cfg *config) begin(description string) {
	fmt.Printf("%s ...\n", description)
	cfg.t0 = time.Now()
	cfg.rpcs0 = cfg.rpcTotal()
	cfg.cmds0 = 0
	cfg.maxIndex0 = cfg.maxIndex
}

func (cfg *config) end() {
	cfg.checkTimeout()
	if cfg.t.Failed() == false {
		cfg.mu.Lock()
		t := time.Since(cfg.t0).Seconds()     // real time
		npeers := cfg.n                       // number of Raft peers
		nrpc := cfg.rpcTotal() - cfg.rpcs0    // number of RPC sends
		ncmds := cfg.maxIndex - cfg.maxIndex0 // number of Raft agreements reported
		cfg.mu.Unlock()

		fmt.Printf("  ... Passed --")
		fmt.Printf("  %4.1f  %d %4d %4d\n", t, npeers, nrpc, ncmds)
	}
}
