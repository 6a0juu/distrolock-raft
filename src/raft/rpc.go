package raft

// RequestVote RPC handler.
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Err, reply.Server = OK, rf.me
	if rf.currentTerm == args.Term && rf.votedFor == args.CandidateId {
		reply.VoteGranted, reply.Term = true, rf.currentTerm
		return
	}
	if rf.currentTerm > args.Term ||
		(rf.currentTerm == args.Term && rf.votedFor != -1) {
		reply.Term, reply.VoteGranted = rf.currentTerm, false
		return
	}
	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
		if rf.state != Follower {
			rf.resetElectionTimer(newRandDuration(ElectionTimeout))
			rf.state = Follower
		}
	}
	rf.leaderId = -1
	reply.Term = args.Term
	lastLogIndex := rf.logIndex - 1
	lastLogTerm := rf.getEntry(lastLogIndex).LogTerm
	if lastLogTerm > args.LastLogTerm ||
		(lastLogTerm == args.LastLogTerm && lastLogIndex > args.LastLogIndex) {
		reply.VoteGranted = false
		return
	}
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	rf.resetElectionTimer(newRandDuration(ElectionTimeout)) // granting vote to candidate, reset electionTimer
	rf.persist()
}

// AppendEntries RPC handler
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.currentTerm > args.Term {
		reply.Term, reply.Success = rf.currentTerm, false
		return
	}
	reply.Term = args.Term
	rf.leaderId = args.LeaderId
	rf.resetElectionTimer(newRandDuration(ElectionTimeout))
	if args.Term > rf.currentTerm {
		rf.currentTerm, rf.votedFor = args.Term, -1
	}
	rf.state = Follower
	logIndex := rf.logIndex
	prevLogIndex := args.PrevLogIndex
	if prevLogIndex < rf.lastIncludedIndex {
		reply.Success, reply.ConflictIndex = false, rf.lastIncludedIndex+1
		return
	}
	if logIndex <= prevLogIndex || rf.getEntry(prevLogIndex).LogTerm != args.PrevLogTerm { // follower don't agree with leader on last log entry
		conflictIndex := Min(rf.logIndex-1, prevLogIndex)
		conflictTerm := rf.getEntry(conflictIndex).LogTerm
		floor := Max(rf.lastIncludedIndex, rf.commitIndex)
		for ; conflictIndex > floor && rf.getEntry(conflictIndex-1).LogTerm == conflictTerm; conflictIndex-- {
		}
		reply.Success, reply.ConflictIndex = false, conflictIndex
		return
	}
	reply.Success, reply.ConflictIndex = true, -1
	i := 0
	for ; i < args.Len; i++ {
		if prevLogIndex+1+i >= rf.logIndex {
			break
		}
		if rf.getEntry(prevLogIndex+1+i).LogTerm != args.Entries[i].LogTerm {
			rf.logIndex = prevLogIndex + 1 + i
			truncationEndIndex := rf.getOffsetIndex(rf.logIndex)
			rf.log = append(rf.log[:truncationEndIndex]) // delete any conflicting log entries
			break
		}
	}
	for ; i < args.Len; i++ {
		rf.log = append(rf.log, args.Entries[i])
		rf.logIndex += 1
	}
	oldCommitIndex := rf.commitIndex
	rf.commitIndex = Max(rf.commitIndex, Min(args.CommitIndex, args.PrevLogIndex+args.Len))
	rf.persist()
	rf.resetElectionTimer(newRandDuration(ElectionTimeout)) // reset electionTimer
	if rf.commitIndex > oldCommitIndex {
		rf.notifyApplyCh <- struct{}{}
	}
}

func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	reply.Err = OK
	reply.Term = rf.currentTerm
	if args.Term < rf.currentTerm {
		return
	}
	rf.leaderId = args.LeaderId
	if args.LastIncludedIndex > rf.lastIncludedIndex {
		truncationStartIndex := rf.getOffsetIndex(args.LastIncludedIndex)
		rf.lastIncludedIndex = args.LastIncludedIndex
		oldCommitIndex := rf.commitIndex
		rf.commitIndex = Max(rf.commitIndex, rf.lastIncludedIndex)
		rf.logIndex = Max(rf.logIndex, rf.lastIncludedIndex+1)
		if truncationStartIndex < len(rf.log) {
			rf.log = append(rf.log[truncationStartIndex:])
		} else {
			rf.log = []LogEntry{{args.LastIncludedIndex, args.LastIncludedTerm, nil}}
		}
		rf.persister.SaveStateAndSnapshot(rf.getPersistState(), args.Data)
		if rf.commitIndex > oldCommitIndex {
			rf.notifyApplyCh <- struct{}{}
		}
	}
	rf.resetElectionTimer(newRandDuration(ElectionTimeout))
	rf.persist()
}
